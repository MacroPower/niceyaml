package tokens_test

import (
	"fmt"
	"iter"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/tokens"
)

func collectDocs(seq iter.Seq2[int, token.Tokens]) []token.Tokens {
	var result []token.Tokens

	for _, tks := range seq {
		result = append(result, tks)
	}

	return result
}

// collectReset collects the documents of seq with their positions reset.
func collectReset(seq iter.Seq2[int, token.Tokens]) []token.Tokens {
	var result []token.Tokens

	for _, tks := range seq {
		result = append(result, tokens.ResetPositions(tks))
	}

	return result
}

func TestTokenize_TabIndentation(t *testing.T) {
	t.Parallel()

	// The lexer swallows characters after a tab used as indentation, so the
	// joined origins are not a prefix of the source. The final line ending
	// must still come back, so the stream ends where the file does.
	tks := tokens.Tokenize("\ta: 1\n")
	require.NotEmpty(t, tks)

	assert.True(t, strings.HasSuffix(tks[len(tks)-1].Origin, "\n"), "last origin %q", tks[len(tks)-1].Origin)
}

func TestTokenize_TabSwallowsEnd(t *testing.T) {
	t.Parallel()

	// When a tab used as indentation opens the file, the lexer's invalid
	// token can swallow the rest of the text. The token keeps the
	// whitespace the lexer gave it, and the whitespace after the swallowed
	// text follows it.
	tcs := map[string]struct {
		input string
		want  string
	}{
		"sequence entry":       {input: "\t-", want: "\t"},
		"mapping value":        {input: "\t:", want: "\t"},
		"line ending":          {input: "\t-\n", want: "\t\n"},
		"blank line":           {input: "\t-\n\n", want: "\t\n\n"},
		"trailing space":       {input: "\t- ", want: "\t "},
		"space before the tab": {input: " \t-", want: " \t"},
		"crlf after two tabs":  {input: "\t\t-\r\n", want: "\t\t\r\n"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := tokens.Tokenize(tc.input)
			require.Len(t, tks, 1)
			assert.Equal(t, token.InvalidType, tks[0].Type)
			assert.Equal(t, tc.want, tks[0].Origin)
		})
	}
}

func TestTokenize_NumericEscape(t *testing.T) {
	t.Parallel()

	// The lexer drops the code of a "\x", "\u", or "\U" escape from a
	// double-quoted scalar's Origin, and Tokenize restores the Origin from
	// the source, so the joined Origins equal the input. An escape that
	// reads the closing quote as a hex digit leaves the scalar open, and the
	// lexer makes an invalid token of the rest of the source. Tokenize
	// restores that token the same way. It does the same for the invalid
	// token of a scalar the lexer cuts at a "---" or "..." line or at an
	// escape it rejects. When
	// the lexer gives up on a "\u" or "\U" escape, it reads the backslash
	// into two tokens, and Tokenize leaves it in the second alone.
	tcs := map[string]struct {
		input string
	}{
		"hex escape": {
			input: `a: "x\x41"` + "\n",
		},
		"short unicode escape": {
			input: `a: "x\u0041"` + "\n",
		},
		"long unicode escape": {
			input: `a: "x\U00000041"` + "\n",
		},
		"text after the escape": {
			input: `a: "x\x41 b"` + "\n",
		},
		"flow mapping": {
			input: `m: {a: "\u00e9", b: xx}` + "\n",
		},
		"trailing comment": {
			input: `a: "\u00e9" # c` + "\n",
		},
		"multi-line scalar": {
			input: `a: "x\x41` + "\n" + `  b c"` + "\n" + "c: 1\n",
		},
		"surrogate pair": {
			input: `a: "\uD83D\uDE00"` + "\n",
		},
		"escaped quote before the escape": {
			input: `a: "\"\u00e9"` + "\n",
		},
		"hex escape over a quote": {
			input: `a: "\x"b"` + "\n",
		},
		"escape the lexer keeps": {
			input: `a: "x\ty"` + "\n",
		},
		"short unicode escape swallows the quote": {
			input: `a: "\u12"` + "\n" + "b: 1\nc: 2\nd: 3\n",
		},
		"hex escape swallows the quote": {
			input: `a: "\x4"` + "\n" + "b: 1\n",
		},
		"unicode escape one digit short": {
			input: `name: "caf\u00e"` + "\n" + "age: 3\n",
		},
		"escape swallows the quote at the end": {
			input: `a: "\x4"`,
		},
		"escape swallows the quote and a cr": {
			input: `a: "\u12"` + "\r\nb: 1\r\n",
		},
		"escape swallows the quote before blank lines": {
			input: `a: "\u12"` + "\n" + "b: 1\n  \n\n",
		},
		"escape swallows the quote of the whole source": {
			input: `"\x4"` + "\n",
		},
		"unicode escape cut short by the end": {
			input: `a: "\u: 1`,
		},
		"long unicode escape cut short by the end": {
			input: `"\U: \U`,
		},
		"unicode escape cut short before the quote": {
			input: `a: "\u1"` + "\n",
		},
		"high surrogate without a low one": {
			input: `a: "\uD83Dxx"` + "\n" + "b: 1\n",
		},
		"escape cut short on a later line": {
			input: `a: "x` + "\n" + `  \u1"` + "\n",
		},
		"escape cut short after an escaped backslash": {
			input: `a: "\\\u"`,
		},
		"open scalar cut at a header": {
			input: `a: "caf\u00e9` + "\n---\nb: 1\n",
		},
		"open scalar cut at a document end": {
			input: `a: "x\x41` + "\n...\nb: 1\n",
		},
		"open scalar cut at a header after an escape code that holds its next rune": {
			input: `a: "caf\u00e9e` + "\n---\nb: 1\n",
		},
		"open scalar cut at a header after a hex escape that holds its next rune": {
			input: `a: "x\x4ee` + "\n---\nb: 1\n",
		},
		"open scalar cut at a header after a surrogate pair": {
			input: `a: "\uD83D\uDE00D` + "\n---\nb: 1\n",
		},
		"open scalar cut at a header in a CRLF file": {
			input: `a: "caf\u00e9` + "\r\n---\r\nb: 1\r\n",
		},
		"open scalar cut at a header after a tag": {
			input: `a: !!str "caf\u00e9` + "\n---\nb: 1\n",
		},
		"open scalar cut at a header after trailing spaces": {
			input: `a: "caf\u00e9   ` + "\n---\nb: 1\n",
		},
		"hex escape swallows the quote before a header": {
			input: `a: "\x1"` + "\n---\nb: 1\n",
		},
		"hex escape swallows the quote before a document end": {
			input: `a: "\x1"` + "\n...\nb: 1\n",
		},
		"unicode escape swallows the quote and a cr before a header": {
			input: `a: "\u12"` + "\r\n---\r\nb: 1\r\n",
		},
		"hex escape swallows the quote and a space before a header": {
			input: `a: "\x" ` + "\n---\nb: 1\n",
		},
		"unicode escape swallows a sequence entry before a header": {
			input: `"\u` + "\n- b\n---\n",
		},
		"escaped backslash before a header": {
			input: `a: "\\` + "\n---\nb: 1\n",
		},
		"escaped backslashes on two lines before a header": {
			input: `{a: "\\` + "\n\t" + `\\` + "\n---\n",
		},
		"escape cut short after a unicode escape": {
			input: `a: "\u00e9\u"`,
		},
		"long escape cut short after a hex escape and a tab": {
			input: `k: "\x41` + "\t" + `\U12"` + "\n",
		},
		"escape cut short after an escaped backslash before hex digits": {
			input: `k: "\x41\\x41\u"` + "\n",
		},
		"high surrogate without a low one after a tab": {
			input: `- ":` + "\t\t" + `\N\uD83D\u` + "\t" + `"` + "\nb: 1\n",
		},
		"long escape that takes a header before a second header": {
			input: `k: "\U12\"` + "\n---\n\n---\n\nb: 1\n",
		},
		"unknown escape after an escape": {
			input: `a: "caf\u00e9 \d"` + "\n",
		},
		"unknown escape on a later line": {
			input: `name: "caf\u00e9` + "\n" + `  x \d"` + "\nnext: 1\n",
		},
		"unknown escape after a comment": {
			input: "a: # c\n" + `  "caf\u00e9 \d"` + "\nb: 1\n",
		},
		"escape cut short after an escape": {
			input: `a: "caf\u00e9 \u1"` + "\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := yamltest.DumpTokenOrigins(tokens.Tokenize(tc.input))
			assert.Equal(t, tc.input, got)
		})
	}
}

func TestTokenize_FinalBlankLines(t *testing.T) {
	t.Parallel()

	// The stream keeps the blank lines that end the file, even when the
	// lexer rewrote the last token, cut it short, or gave it whitespace
	// alone. The whitespace comes back once, so a block scalar header that
	// holds the start of it does not repeat it. A last token that sits in
	// front of text the lexer dropped keeps the line ending it holds.
	tcs := map[string]struct {
		input string
		// The joined Origins equal the input.
		whole bool
	}{
		"trailing space inside the last scalar": {
			input: "description: first line \n  second line\n\n",
		},
		"blank line of spaces inside the last scalar": {
			input: "description: first\n  \n  second\n\n",
		},
		"several final blank lines": {
			input: "k: plain\n   \n  more\n\n\n",
		},
		"sequence entry": {
			input: "- a\n  \n  b\n\n",
		},
		"invalid tab token": {
			input: "a:\n\t\n\n",
			whole: true,
		},
		"invalid tab token after a header": {
			input: "a: |\n\t\n\n",
			whole: true,
		},
		"truncated escape": {
			input: `a: "\x41"` + "\n\n",
			whole: true,
		},
		"empty block scalar content": {
			input: "a: |\n\n",
			whole: true,
		},
		"empty block scalar content crlf": {
			input: "a: |+\r\n\r\n",
			whole: true,
		},
		"empty block scalar in a sequence": {
			input: "- |\n\n",
			whole: true,
		},
		"header with trailing spaces": {
			input: "a: |  \n  \n \n",
			whole: true,
		},
		"dropped text ends the file": {
			input: "a: 1\n!",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := tokens.Tokenize(tc.input)
			require.NotEmpty(t, tks)

			got := yamltest.DumpTokenOrigins(tks)
			assert.Equal(t, countLineBreaks(tc.input), countLineBreaks(got), "joined origins %q", got)

			tail := tc.input[len(strings.TrimRight(tc.input, " \t\r\n")):]
			assert.True(t, strings.HasSuffix(got, tail), "joined origins %q", got)

			if tc.whole {
				assert.Equal(t, tc.input, got)
			}
		})
	}
}

func TestTokenize_ByteOrderMark(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
		// The line and column of the key b, when the input holds one.
		line, col int
	}{
		"start of the stream": {
			input: "\ufeffa: 1\n",
			want:  "a: 1\n",
		},
		"before a comment": {
			input: "\ufeff# c\na: 1\n",
			want:  "# c\na: 1\n",
		},
		"after a comment cr": {
			input: "\ufeff# c\r\ufeffa: 1\r",
			want:  "# c\ra: 1\r",
		},
		"after a document marker": {
			input: "a: 1\n---\n\ufeffb: 2\n",
			want:  "a: 1\n---\nb: 2\n",
			line:  3,
			col:   1,
		},
		"after a document marker crlf": {
			input: "a: 1\r\n---\r\n\ufeffb: 2\r\n",
			want:  "a: 1\r\n---\r\nb: 2\r\n",
			line:  3,
			col:   1,
		},
		"after a document marker cr": {
			input: "a: 1\r---\r\ufeffb: 2\r",
			want:  "a: 1\r---\rb: 2\r",
			line:  3,
			col:   1,
		},
		"after a document marker and a comment": {
			input: "a: 1\n--- # c\n# d\n\ufeffb: 2\n",
			want:  "a: 1\n--- # c\n# d\nb: 2\n",
			line:  4,
			col:   1,
		},
		"before a document marker": {
			input: "\ufeff---\na: 1\n\ufeff---\n\ufeffb: 2\n",
			want:  "---\na: 1\n---\nb: 2\n",
			line:  4,
			col:   1,
		},
		"after a document end marker": {
			input: "a: 1\n...\n\ufeffb: 2\n",
			want:  "a: 1\n...\nb: 2\n",
			line:  3,
			col:   1,
		},
		"after a document end marker cr": {
			input: "a: 1\r...\r\ufeffb: 2\r",
			want:  "a: 1\r...\rb: 2\r",
			line:  3,
			col:   1,
		},
		"inside a document": {
			input: "a: 1\n\ufeffb: 2\n",
			want:  "a: 1\n\ufeffb: 2\n",
		},
		"inside a document mixed endings": {
			input: "# c\ra: 1\n\ufeffb: 2\n",
			want:  "# c\ra: 1\n\ufeffb: 2\n",
		},
		"after a marker with content": {
			input: "--- a\n\ufeffb\n",
			want:  "--- a\n\ufeffb\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := tokens.Tokenize(tc.input)
			assert.Equal(t, tc.want, yamltest.DumpTokenOrigins(tks))

			if tc.line == 0 {
				return
			}

			idx := slices.IndexFunc(tks, func(tk *token.Token) bool { return tk.Value == "b" })
			require.GreaterOrEqual(t, idx, 0)
			assert.Equal(t, tc.line, tks[idx].Position.Line)
			assert.Equal(t, tc.col, tks[idx].Position.Column)
		})
	}
}

func TestIsPlaceholder(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		idx   int
		want  bool
	}{
		"whitespace only":               {input: "\n  \n", want: true},
		"lone tag marker":               {input: "!", want: true},
		"plain scalar":                  {input: "abc", want: false},
		"plain scalar line":             {input: "abc\n", want: false},
		"mapping":                       {input: "a: 1\n", want: false},
		"empty block scalar content":    {input: "a: |\nb: 1\n", idx: 3, want: false},
		"empty folded scalar content":   {input: "a: >\nb: 1\n", idx: 3, want: false},
		"kept blank block scalar lines": {input: "a: |+\n\n\nb: 1\n", idx: 3, want: false},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := tokens.Tokenize(tc.input)
			require.Greater(t, len(tks), tc.idx)
			assert.Equal(t, tc.want, tokens.IsPlaceholder(tks[tc.idx]))
		})
	}

	assert.False(t, tokens.IsPlaceholder(nil))
}

func TestTokenize_EmptyContentPastEnd(t *testing.T) {
	t.Parallel()

	// The lexer places the empty content of a block scalar that keeps
	// its trailing lines on the line after the header. When the header
	// ends the file, that line does not exist, and the token moves to
	// the end of the header's line. Content of blank lines holds no text
	// either, so when those lines end the file, the token moves to the
	// end of the last line, even after an escape the lexer gives up on
	// and reads again. A line the lexer dropped, such as a lone "!", still
	// exists, and the token stays on it.
	tcs := map[string]struct {
		input  string
		line   int
		col    int
		offset int
	}{
		"header ends the file":      {input: "a: |+\n", line: 1, col: 6, offset: 6},
		"header ends the file crlf": {input: "a: |+\r\n", line: 1, col: 6, offset: 6},
		"header ends the file cr":   {input: "a: |+\r", line: 1, col: 6, offset: 6},
		"sequence entry":            {input: "- >+\n", line: 1, col: 5, offset: 5},
		"second header ends it":     {input: "x: \"\u00e9\"\nk: |+\n", line: 2, col: 6, offset: 13},
		"dropped line follows":      {input: "a: |+\n!", line: 2, col: 1, offset: 7},
		"blank line ends the file":  {input: "a: |+\n\n", line: 2, col: 1, offset: 7},
		"blank lines end the file":  {input: "a: |+\n\n\n", line: 3, col: 1, offset: 8},
		"blank crlf line ends it":   {input: "a: |+\r\n\r\n", line: 2, col: 1, offset: 8},
		"spaces end the file":       {input: "a: >+\n  \n", line: 2, col: 3, offset: 9},
		"clipped blank line ends":   {input: "a: |\n\n", line: 2, col: 1, offset: 6},
		"key follows":               {input: "a: |+\nb: 1\n", line: 2, col: 1, offset: 7},
		"header after a cut escape": {input: "\"\\u>+: |\n\n", line: 2, col: 1, offset: 10},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var empty *token.Token

			for _, tk := range tokens.Tokenize(tc.input) {
				if strings.TrimSpace(tk.Origin) == "" && empty == nil {
					empty = tk
				}
			}

			require.NotNil(t, empty)
			assert.Equal(t, tc.line, empty.Position.Line)
			assert.Equal(t, tc.col, empty.Position.Column)
			assert.Equal(t, tc.offset, empty.Position.Offset)
		})
	}
}

func TestTokenize_PlaceholderPosition(t *testing.T) {
	t.Parallel()

	// The placeholder sits where its text starts, like any other token,
	// and at 1:1:1 when the source holds whitespace alone. A fresh stream
	// already counts from 1, so ResetPositions keeps its position.
	tcs := map[string]struct {
		input  string
		line   int
		col    int
		offset int
	}{
		"lone tag marker":            {input: "!", line: 1, col: 1, offset: 1},
		"spaces before":              {input: "  !", line: 1, col: 3, offset: 3},
		"tab before":                 {input: "\t!", line: 1, col: 2, offset: 2},
		"line break before":          {input: "\n!", line: 2, col: 1, offset: 2},
		"indented after a break":     {input: "\n  !", line: 2, col: 3, offset: 4},
		"indented after two breaks":  {input: "\n\n  !", line: 3, col: 3, offset: 5},
		"tab after two breaks":       {input: "\n\n\t!", line: 3, col: 2, offset: 4},
		"whitespace alone":           {input: "  \n", line: 1, col: 1, offset: 1},
		"line breaks alone":          {input: "\n\n", line: 1, col: 1, offset: 1},
		"byte order mark is dropped": {input: "\ufeff  !", line: 1, col: 3, offset: 3},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := tokens.Tokenize(tc.input)
			require.Len(t, tks, 1)
			require.True(t, tokens.IsPlaceholder(tks[0]))

			assert.Equal(t, tc.line, tks[0].Position.Line)
			assert.Equal(t, tc.col, tks[0].Position.Column)
			assert.Equal(t, tc.offset, tks[0].Position.Offset)

			reset := tokens.ResetPositions(tks)
			require.Len(t, reset, 1)
			assert.Equal(t, *tks[0].Position, *reset[0].Position)
		})
	}
}

func TestTokenize_RepairsPositionsAfterTruncatedLastToken(t *testing.T) {
	t.Parallel()

	// The lexer places a token one rune short after a tag. When the last
	// token's text is not in the source, every position must still move to
	// where the source holds the text.
	tks := tokens.Tokenize("a: !!str x\nname: \"Caf\\u00e9\"\n")
	require.NotEmpty(t, tks)

	var x *token.Token

	for _, tk := range tks {
		if tk.Value == "x" {
			x = tk
		}
	}

	require.NotNil(t, x)
	assert.Equal(t, 1, x.Position.Line)
	assert.Equal(t, 10, x.Position.Column)
	assert.Equal(t, 10, x.Position.Offset)
}

func TestTokenize_RestoresDroppedLineBreaks(t *testing.T) {
	t.Parallel()

	// The lexer drops the line breaks and indentation between some tokens
	// and a "?" or ":" indicator that follows them, and a blank line from
	// a run of them after a double-quoted scalar that holds a tab.
	// Tokenize puts them back, so the Origins ahead of each token's text
	// hold as many line breaks as the source does. The want field holds
	// the joined Origins where they differ from the input, around the
	// invalid token the lexer makes for a tab on a blank line.
	tcs := map[string]struct {
		input string
		want  string
	}{
		"key after a flow sequence": {
			input: "tags: [a, b]\n\n? key\n: value\n",
		},
		"value after a flow mapping": {
			input: "a: {x: y}\n\n: v\n",
		},
		"key after a quoted scalar": {
			input: "a: 'x'\n\n? b\n: c\n",
		},
		"key after an alias": {
			input: "a: *x\n\n? b\n",
		},
		"key after an empty value": {
			input: "a:\n\n? b\n: c\n",
		},
		"key after a document header": {
			input: "---\n\n? b\n",
		},
		"key after a line comment": {
			input: "a: 1 # note\n\n? k\n",
		},
		"key after a comment": {
			input: "# c\n\n? k\n",
		},
		"key after a blank line": {
			input: "\n? k\n: v\n",
		},
		"key after blank lines": {
			input: "\n\n? b\n: c\n",
		},
		"nested key after a flow sequence": {
			input: "a:\n  b: [x]\n\n  ? k\n  : v\n",
		},
		"crlf key after a comment": {
			input: "# c\r\n\r\n? k\r\n",
		},
		"plain scalar after a blank line of spaces": {
			input: "t: \"a\tb\"\n  \n  r'\n- e\n",
		},
		"plain scalar after blank lines of spaces": {
			input: "t: \"a\tb\"\n  \n  \n  r'\n- e\n",
		},
		"key after blank lines of spaces": {
			input: "a: \"t\tb\"\n  \n  \nc: 1\n",
		},
		"comment after blank lines of spaces": {
			input: "a: \"t\tb\"\n  \n  \n# c\nd: 1\ne: 2\n",
		},
		"crlf key after a blank line the lexer rewrites": {
			// The lexer turns the CRLF in front of the invalid blank line
			// into "\n".
			input: "x:\r\n  a: \"t\tb\"\r\n \t\r\n  \r\n? b\r\n",
			want:  "x:\r\n  a: \"t\tb\"\n \t\r\n\r\n? b\r\n",
		},
		"key after a blank line with a tab": {
			// The blank line goes to the end of the scalar's Origin. A
			// tab in front of a line break in the key's Origin would
			// make the parser reject the key.
			input: "a: \"t\tb\"\n \t\nb: 1\n",
		},
		"key after trailing spaces the lexer drops": {
			// The lexer drops the spaces after the flow mapping and makes
			// an invalid token of the blank line, whose indentation must
			// not repeat. Tokenize leaves the whitespace around that
			// token as the lexer made it.
			input: "{a: b}  \n \t\n? k\n",
			want:  "{a: b}\n \t\n? k\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			runes := []rune(tc.input)

			var joined strings.Builder

			for i, tk := range tokens.Tokenize(tc.input) {
				require.NotNil(t, tk.Position, "token %d", i)

				text := firstTextLine(tk.Origin)
				if text != "" {
					// The line breaks in the Origins ahead of the token
					// and in the whitespace its own Origin opens with add
					// up to the lines above its text.
					lead := tk.Origin[:len(tk.Origin)-len(strings.TrimLeft(tk.Origin, " \t\r\n"))]
					assert.Equal(
						t,
						tk.Position.Line,
						1+countLineBreaks(joined.String()+lead),
						"token %d %q origins",
						i,
						tk.Origin,
					)

					// The position names the rune of the source where the
					// text starts.
					at := tk.Position.Offset - 1
					require.GreaterOrEqual(t, at, 0, "token %d %q", i, tk.Origin)
					require.LessOrEqual(t, at, len(runes), "token %d %q", i, tk.Origin)

					before := string(runes[:at])
					lastBreak := strings.LastIndexAny(before, "\r\n")

					assert.Equal(t, 1+countLineBreaks(before), tk.Position.Line, "token %d %q line", i, tk.Origin)

					wantCol := utf8.RuneCountInString(before[lastBreak+1:]) + 1
					assert.Equal(t, wantCol, tk.Position.Column, "token %d %q column", i, tk.Origin)
					assert.True(t, strings.HasPrefix(string(runes[at:]), text), "token %d %q at %d", i, tk.Origin, at)
				}

				joined.WriteString(tk.Origin)
			}

			want := tc.want
			if want == "" {
				want = tc.input
			}

			assert.Equal(t, want, joined.String())
		})
	}
}

func TestTokenize(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
	}{
		"empty string": {
			input: "",
		},
		"simple key value": {
			input: "key: value\n",
		},
		"multi-line": {
			input: stringtest.JoinLF(
				"key1: value1",
				"key2: value2",
				"key3: value3",
			),
		},
		"nested structure": {
			input: stringtest.JoinLF(
				"parent:",
				"  child1: value1",
				"  child2: value2",
			),
		},
		"unicode content": {
			input: "greeting: こんにちは\n",
		},
		"whitespace only": {
			input: "\n\n",
		},
		"spaces and tabs only": {
			input: " \t \n",
		},
		"trailing blank line": {
			input: "key: value\n\n",
		},
		"crlf trailing blank line": {
			input: "key: value\r\n\r\n",
		},
		"trailing blank line of spaces": {
			input: "key: value\n   \n\n",
		},
		"trailing line of spaces": {
			input: "key: value\n  \n",
		},
		"list": {
			input: stringtest.JoinLF(
				"items:",
				"  - one",
				"  - two",
				"  - three",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want := lexer.Tokenize(tc.input)
			got := tokens.Tokenize(tc.input)

			// The stream covers the whole source, including the final
			// line ending the lexer leaves out of the last Origin.
			var joined strings.Builder

			for _, tk := range got {
				joined.WriteString(tk.Origin)
			}

			assert.Equal(t, tc.input, joined.String())

			if len(want) == 0 {
				// The lexer emits nothing for whitespace alone, and
				// Tokenize covers such a source with one token.
				if tc.input == "" {
					assert.Empty(t, got)
				} else {
					assert.Len(t, got, 1)
				}

				return
			}

			// The lexer rewrites the whitespace that closes the last
			// Origin, collapsing a blank line of spaces to a bare line
			// ending, so only the text ahead of that whitespace survives
			// into the restored Origin.
			last := len(want) - 1
			lastText := strings.TrimRight(want[last].Origin, " \t\r\n")
			assert.True(t, strings.HasPrefix(got[last].Origin, lastText))

			want[last].Origin = got[last].Origin

			yamltest.RequireTokensEqual(t, want, got)
		})
	}
}

func TestTokenize_RestoresDroppedWhitespace(t *testing.T) {
	t.Parallel()

	// The lexer drops the spaces and tabs that end a line, the spaces of a
	// blank line, the space in front of a ":" after a quoted or alias key,
	// the space between a "-" and a "?", and the line breaks in front of a
	// "?" key. Tokenize gives them back, so the joined Origins equal the
	// input. The want field holds the joined Origins where they differ
	// from the input.
	tcs := map[string]struct {
		input string
		want  string
	}{
		"space before a colon after quoted flow keys": {
			input: "{\"a\" : 1, \"b\" : \"x\"}\n",
		},
		"space before a colon after an alias key": {
			input: "&a a : 1\n*a : 2\n",
		},
		"space before a colon after a single-quoted key": {
			input: "'a' : 1\n",
		},
		"nested explicit key": {
			input: "a:\n  ? zz\n  : x\n",
		},
		"explicit key in a sequence entry": {
			input: "- ? earth\n  : blue\n",
		},
		"explicit key after a document header": {
			input: "x: 1\n---\n\n\n? a\n: b\n",
		},
		"explicit key after a flow sequence": {
			input: "tags: [a, b]\n\n? key\n: value\n",
		},
		"explicit key after a comment": {
			input: "# c\n\n? b\n: c\n",
		},
		"explicit key after a blank line": {
			input: "\n? k\n: v\n",
		},
		"crlf explicit key after a comment": {
			input: "# c\r\n\r\n? b\r\n",
		},
		"trailing tab": {
			input: "a: 1\t\nb: 2\n",
		},
		"trailing spaces": {
			input: "a: 1   \nb: 2\n",
		},
		"trailing spaces after a quoted scalar": {
			input: "a: 'x'   \nb: 1\n",
		},
		"trailing spaces after an anchor": {
			input: "a: &k   \n  b: 1\n",
		},
		"trailing spaces after a tag": {
			input: "a: !t   \n  b: 1\n",
		},
		"trailing spaces after a flow sequence": {
			input: "a: [1, 2]   \nb: 2\n",
		},
		"blank line of spaces": {
			input: "a: 1\n   \nb: 2\n",
		},
		"blank line of spaces after a document end": {
			input: "a: 1\n...\n  \nb: 2\n",
		},
		"blank line of spaces in a plain scalar": {
			input: "a: plain\n   \n  multi\nb: 2\n",
		},
		"blank line with a tab before the first key": {
			input: " \t\na: 1\n",
		},
		"blank line of spaces after a bare cr": {
			input: "\r \na: 1\n",
		},
		"blank lines of spaces after a bare cr in a mapping": {
			input: "a:\r  \r \n  b: 2\n",
		},
		"line ending the lexer repeats after a tag": {
			// The source holds one line break where the Origins hold two,
			// so Tokenize keeps the Origins the lexer made.
			input: "u: !t\n  v: 1\n",
			want:  "u: !t\n\n  v: 1\n",
		},
		"blank line after a line ending the lexer repeats": {
			// Tokenize keeps the Origins the lexer made around the
			// repeat, so the blank line loses its spaces.
			input: "a: !t\n  \n  b: 1\n",
			want:  "a: !t\n\n\n  b: 1\n",
		},
		"line ending the lexer repeats after a block scalar header": {
			// The lexer makes an invalid token of the header and the
			// text after it, and repeats the line ending that closes it.
			input: "a: | x\n  b\n",
			want:  "a: | x\n\n  b\n",
		},
		"bad block header at the end": {
			// The lexer repeats the header's last rune as a token of its
			// own when no line ending follows, and Tokenize drops it.
			input: "key: >foo",
		},
		"bad block header and comment at the end": {
			input: "k: |ab # c",
		},
		"bad block header in a later entry": {
			// The line ending after the first header repeats as well.
			input: "- |ab\n- |ab",
			want:  "- |ab\n\n- |ab",
		},
		"text after a block scalar header at the end": {
			// The lexer reads the last rune of the text a second time,
			// and Tokenize drops the token it makes of it.
			input: "key: |abc",
		},
		"text after a folded scalar header at the end": {
			input: "a: >x y",
		},
		"lone tag marker at the end": {
			// The lexer drops the "!" and the spaces in front of it.
			input: "a: !",
			want:  "a:",
		},
		"lone tag marker on the last line": {
			input: "a: b\n!",
			want:  "a: b\n",
		},
		"lone tag marker alone": {
			// The placeholder token holds the whole source.
			input: "!",
		},
		"alias after a block scalar header at the end": {
			input: "|*x",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want := tc.want
			if want == "" {
				want = tc.input
			}

			assert.Equal(t, want, yamltest.DumpTokenOrigins(tokens.Tokenize(tc.input)))
		})
	}
}

func TestForParser(t *testing.T) {
	t.Parallel()

	// ForParser cuts the blank lines in front of the first text down to
	// their line breaks, and every other Origin and every position stays.
	// The tokens passed in keep their Origins.
	tcs := map[string]struct {
		input string
		want  string
	}{
		"blank line of a tab": {
			input: "\t\nb: 1\n",
			want:  "\nb: 1\n",
		},
		"indented key after a blank line with a tab": {
			input: " \t \n  b: 1\n",
			want:  "\n  b: 1\n",
		},
		"crlf blank lines": {
			input: "\t\r\n \r\nb: 1\r\n",
			want:  "\r\n\r\nb: 1\r\n",
		},
		"comment after a blank line of a tab": {
			input: "\t\n# c\nb: 1\n",
			want:  "\n# c\nb: 1\n",
		},
		"blank line of spaces after the first key": {
			input: "a: 1\n  \nb: 2\n",
			want:  "a: 1\n  \nb: 2\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := tokens.Tokenize(tc.input)
			got := tokens.ForParser(tks)

			assert.Equal(t, tc.want, yamltest.DumpTokenOrigins(got))
			assert.Equal(t, tc.input, yamltest.DumpTokenOrigins(tks))

			require.Len(t, got, len(tks))

			for i := range got {
				assert.NotSame(t, tks[i], got[i], "token %d", i)
				assert.Equal(t, *tks[i].Position, *got[i].Position, "token %d", i)
			}
		})
	}
}

func TestForParser_Links(t *testing.T) {
	t.Parallel()

	// The clones of a document cut from a longer stream link to nothing
	// outside the result, and ForParser drops nil tokens.
	docs := collectDocs(tokens.SplitDocuments(tokens.Tokenize("a: 1\n---\nb: 2\n")))
	require.Len(t, docs, 2)
	require.NotNil(t, docs[1][0].Prev)

	got := tokens.ForParser(append(token.Tokens{nil}, docs[1]...))
	require.Len(t, got, len(docs[1]))

	assert.Nil(t, got[0].Prev)
	assert.Nil(t, got[len(got)-1].Next)

	for i := 1; i < len(got); i++ {
		assert.Same(t, got[i-1], got[i].Prev, "token %d", i)
		assert.Same(t, got[i], got[i-1].Next, "token %d", i-1)
	}
}

func TestSplitDocuments(t *testing.T) {
	t.Parallel()

	t.Run("nil input", func(t *testing.T) {
		t.Parallel()

		got := collectDocs(tokens.SplitDocuments(nil))

		assert.Empty(t, got)
	})

	t.Run("empty slice", func(t *testing.T) {
		t.Parallel()

		got := collectDocs(tokens.SplitDocuments(token.Tokens{}))

		assert.Empty(t, got)
	})

	t.Run("single doc no header", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
			tkb.Clone().Type(token.StringType).Value("value").Build(),
		}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 1)
		require.Len(t, got[0], 3)

		yamltest.RequireTokensEqual(t, input, got[0])
	})

	t.Run("nil tokens are skipped", func(t *testing.T) {
		t.Parallel()

		// Positions start at 1:1 so a reset leaves them unchanged.
		tkb := yamltest.NewTokenBuilder().PositionLine(1).PositionColumn(1)
		want := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").Origin("key").PositionOffset(1).Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Origin(":").PositionOffset(4).Build(),
			tkb.Clone().Type(token.StringType).Value("value").Origin(" value").PositionOffset(6).Build(),
		}
		input := token.Tokens{nil, want[0], want[1], nil, want[2], nil}

		for name, collect := range map[string]func(iter.Seq2[int, token.Tokens]) []token.Tokens{
			"shared": collectDocs,
			"reset":  collectReset,
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got := collect(tokens.SplitDocuments(input))

				require.Len(t, got, 1)

				yamltest.RequireTokensEqual(t, want, got[0])
			})
		}
	})

	t.Run("single doc with header", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build(),
			tkb.Clone().Type(token.StringType).Value("key").Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
			tkb.Clone().Type(token.StringType).Value("value").Build(),
		}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 1)
		require.Len(t, got[0], 4)

		yamltest.RequireTokensEqual(t, input, got[0])
	})

	t.Run("end marker closes a document", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("a").Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
			tkb.Clone().Type(token.StringType).Value("1").Build(),
			tkb.Clone().Type(token.DocumentEndType).Value("...").Build(),
			tkb.Clone().Type(token.StringType).Value("b").Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
			tkb.Clone().Type(token.StringType).Value("2").Build(),
		}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 2)
		require.Len(t, got[0], 4)
		require.Len(t, got[1], 3)

		yamltest.RequireTokensEqual(t, input[:4], got[0])

		yamltest.RequireTokensEqual(t, input[4:], got[1])
	})

	t.Run("end marker followed by header", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("a").Build(),
			tkb.Clone().Type(token.DocumentEndType).Value("...").Build(),
			tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build(),
			tkb.Clone().Type(token.StringType).Value("b").Build(),
		}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 2)
		require.Len(t, got[0], 2)
		require.Len(t, got[1], 2)
		assert.Equal(t, token.DocumentHeaderType, got[1][0].Type)
	})

	t.Run("two docs", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		key1 := tkb.Clone().Type(token.StringType).Value("key1").Build()
		colon1 := tkb.Clone().Type(token.MappingValueType).Value(":").Build()
		value1 := tkb.Clone().Type(token.StringType).Value("v1").Build()
		header := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		key2 := tkb.Clone().Type(token.StringType).Value("key2").Build()
		colon2 := tkb.Clone().Type(token.MappingValueType).Value(":").Build()
		value2 := tkb.Clone().Type(token.StringType).Value("v2").Build()

		input := token.Tokens{key1, colon1, value1, header, key2, colon2, value2}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 2)
		require.Len(t, got[0], 3)
		require.Len(t, got[1], 4)

		yamltest.RequireTokensEqual(t, token.Tokens{key1, colon1, value1}, got[0])

		yamltest.RequireTokensEqual(t, token.Tokens{header, key2, colon2, value2}, got[1])
	})

	t.Run("three docs with headers", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		header1 := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		doc1 := tkb.Clone().Type(token.StringType).Value("doc1").Build()
		header2 := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		doc2 := tkb.Clone().Type(token.StringType).Value("doc2").Build()
		header3 := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		doc3 := tkb.Clone().Type(token.StringType).Value("doc3").Build()

		input := token.Tokens{header1, doc1, header2, doc2, header3, doc3}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 3)

		yamltest.RequireTokensEqual(t, token.Tokens{header1, doc1}, got[0])

		yamltest.RequireTokensEqual(t, token.Tokens{header2, doc2}, got[1])

		yamltest.RequireTokensEqual(t, token.Tokens{header3, doc3}, got[2])
	})

	t.Run("doc with end marker", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
			tkb.Clone().Type(token.StringType).Value("value").Build(),
			tkb.Clone().Type(token.DocumentEndType).Value("...").Build(),
		}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 1)
		require.Len(t, got[0], 4)

		yamltest.RequireTokensEqual(t, input, got[0])
	})

	t.Run("doc end followed by new doc", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		doc1 := tkb.Clone().Type(token.StringType).Value("doc1").Build()
		docEnd := tkb.Clone().Type(token.DocumentEndType).Value("...").Build()
		header := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		doc2 := tkb.Clone().Type(token.StringType).Value("doc2").Build()

		input := token.Tokens{doc1, docEnd, header, doc2}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 2)

		yamltest.RequireTokensEqual(t, token.Tokens{doc1, docEnd}, got[0])

		yamltest.RequireTokensEqual(t, token.Tokens{header, doc2}, got[1])
	})

	t.Run("early termination at first doc", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		doc1 := tkb.Clone().Type(token.StringType).Value("doc1").Build()
		header := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		doc2 := tkb.Clone().Type(token.StringType).Value("doc2").Build()

		input := token.Tokens{doc1, header, doc2}

		var got []token.Tokens

		for _, tks := range tokens.SplitDocuments(input) {
			got = append(got, tks)

			break // Early termination after first document.
		}

		require.Len(t, got, 1)

		yamltest.RequireTokensEqual(t, token.Tokens{doc1}, got[0])
	})

	t.Run("early termination at second doc", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		doc1 := tkb.Clone().Type(token.StringType).Value("doc1").Build()
		header1 := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		doc2 := tkb.Clone().Type(token.StringType).Value("doc2").Build()
		header2 := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		doc3 := tkb.Clone().Type(token.StringType).Value("doc3").Build()

		input := token.Tokens{doc1, header1, doc2, header2, doc3}

		var got []token.Tokens

		count := 0
		for _, tks := range tokens.SplitDocuments(input) {
			got = append(got, tks)
			count++
			if count == 2 {
				break // Early termination after second document.
			}
		}

		require.Len(t, got, 2)

		yamltest.RequireTokensEqual(t, token.Tokens{doc1}, got[0])

		yamltest.RequireTokensEqual(t, token.Tokens{header1, doc2}, got[1])
	})

	t.Run("early termination single doc", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("only").Build(),
		}

		var got []token.Tokens

		for _, tks := range tokens.SplitDocuments(input) {
			got = append(got, tks)

			break // Early termination on single document.
		}

		require.Len(t, got, 1)
		require.Len(t, got[0], 1)
	})
}

func TestResetPositions(t *testing.T) {
	t.Parallel()

	t.Run("single doc resets to line 1", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").
				PositionLine(5).PositionColumn(3).PositionOffset(100).Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").
				PositionLine(5).PositionColumn(6).PositionOffset(103).Build(),
			tkb.Clone().Type(token.StringType).Value("value").
				PositionLine(5).PositionColumn(8).PositionOffset(105).Build(),
		}

		got := collectReset(tokens.SplitDocuments(input))

		require.Len(t, got, 1)
		require.Len(t, got[0], 3)

		// All tokens should be on line 1 now.
		assert.Equal(t, 1, got[0][0].Position.Line)
		assert.Equal(t, 1, got[0][0].Position.Column)
		assert.Equal(t, 1, got[0][0].Position.Offset)

		assert.Equal(t, 1, got[0][1].Position.Line)
		assert.Equal(t, 4, got[0][1].Position.Column)
		assert.Equal(t, 4, got[0][1].Position.Offset)

		assert.Equal(t, 1, got[0][2].Position.Line)
		assert.Equal(t, 6, got[0][2].Position.Column)
		assert.Equal(t, 6, got[0][2].Position.Offset)
	})

	t.Run("multi doc each starts at line 1", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		// First document at line 1.
		doc1Key := tkb.Clone().Type(token.StringType).Value("key1").
			PositionLine(1).PositionColumn(1).PositionOffset(0).Build()
		// Second document starts at line 3.
		header := tkb.Clone().Type(token.DocumentHeaderType).Value("---").
			PositionLine(3).PositionColumn(1).PositionOffset(15).Build()
		doc2Key := tkb.Clone().Type(token.StringType).Value("key2").
			PositionLine(4).PositionColumn(1).PositionOffset(20).Build()

		input := token.Tokens{doc1Key, header, doc2Key}

		got := collectReset(tokens.SplitDocuments(input))

		require.Len(t, got, 2)

		// First document should start at line 1.
		require.Len(t, got[0], 1)
		assert.Equal(t, 1, got[0][0].Position.Line)
		assert.Equal(t, 1, got[0][0].Position.Column)
		assert.Equal(t, 1, got[0][0].Position.Offset)

		// Second document should also start at line 1.
		require.Len(t, got[1], 2)
		assert.Equal(t, 1, got[1][0].Position.Line) // Header.
		assert.Equal(t, 1, got[1][0].Position.Column)
		assert.Equal(t, 1, got[1][0].Position.Offset)

		assert.Equal(t, 2, got[1][1].Position.Line) // Key on next line.
		assert.Equal(t, 1, got[1][1].Position.Column)
		assert.Equal(t, 6, got[1][1].Position.Offset)
	})

	t.Run("preserves original tokens when option not used", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").
				PositionLine(5).PositionColumn(3).PositionOffset(100).Build(),
		}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 1)
		require.Len(t, got[0], 1)

		// Position should be unchanged.
		assert.Equal(t, 5, got[0][0].Position.Line)
		assert.Equal(t, 3, got[0][0].Position.Column)
		assert.Equal(t, 100, got[0][0].Position.Offset)

		// Should be the same pointer (not cloned).
		assert.Same(t, input[0], got[0][0])
	})

	t.Run("clones tokens when reset", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		original := tkb.Clone().Type(token.StringType).Value("key").
			PositionLine(5).PositionColumn(3).PositionOffset(100).Build()
		input := token.Tokens{original}

		got := collectReset(tokens.SplitDocuments(input))

		require.Len(t, got, 1)
		require.Len(t, got[0], 1)

		// Should be a different pointer (cloned).
		assert.NotSame(t, original, got[0][0])

		// Original should be unchanged.
		assert.Equal(t, 5, original.Position.Line)
		assert.Equal(t, 3, original.Position.Column)
		assert.Equal(t, 100, original.Position.Offset)
	})

	t.Run("handles multiline tokens within doc", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		// A multiline block scalar starting at line 10.
		blockIndicator := tkb.Clone().Type(token.LiteralType).Value("|").
			PositionLine(10).PositionColumn(5).PositionOffset(50).Build()
		blockContent := tkb.Clone().Type(token.StringType).Value("line1\nline2").
			PositionLine(11).PositionColumn(5).PositionOffset(52).Build()

		input := token.Tokens{blockIndicator, blockContent}

		got := collectReset(tokens.SplitDocuments(input))

		require.Len(t, got, 1)
		require.Len(t, got[0], 2)

		// Block indicator should be at line 1.
		assert.Equal(t, 1, got[0][0].Position.Line)
		assert.Equal(t, 1, got[0][0].Position.Column)
		assert.Equal(t, 1, got[0][0].Position.Offset)

		// Content should be at line 2 (relative to doc start).
		assert.Equal(t, 2, got[0][1].Position.Line)
		assert.Equal(t, 5, got[0][1].Position.Column)
		assert.Equal(t, 3, got[0][1].Position.Offset)
	})

	t.Run("keeps a blank line with a tab before the first key", func(t *testing.T) {
		t.Parallel()

		// The first token opens with the whole blank line, so a stream
		// that starts at line 1 keeps its positions.
		got := tokens.ResetPositions(tokens.Tokenize(" \t\na: 1\n"))

		require.NotEmpty(t, got)
		assert.Equal(t, " \t\na", got[0].Origin)
		assert.Equal(t, 2, got[0].Position.Line)
		assert.Equal(t, 1, got[0].Position.Column)
		assert.Equal(t, 4, got[0].Position.Offset)
	})

	t.Run("handles nil position", func(t *testing.T) {
		t.Parallel()

		// Token with nil position should not cause panic.
		input := token.Tokens{
			&token.Token{Type: token.StringType, Value: "test", Position: nil},
		}

		got := collectReset(tokens.SplitDocuments(input))

		require.Len(t, got, 1)
		require.Len(t, got[0], 1)
		assert.Nil(t, got[0][0].Position)
	})

	t.Run("handles empty input", func(t *testing.T) {
		t.Parallel()

		got := collectReset(tokens.SplitDocuments(nil))

		assert.Empty(t, got)
	})

	t.Run("three docs all reset independently", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		// Doc 1 at line 1.
		doc1 := tkb.Clone().Type(token.StringType).Value("doc1").
			PositionLine(1).PositionColumn(1).PositionOffset(0).Build()
		// Doc 2 at line 5.
		header2 := tkb.Clone().Type(token.DocumentHeaderType).Value("---").
			PositionLine(5).PositionColumn(1).PositionOffset(20).Build()
		doc2 := tkb.Clone().Type(token.StringType).Value("doc2").
			PositionLine(6).PositionColumn(1).PositionOffset(25).Build()
		// Doc 3 at line 10.
		header3 := tkb.Clone().Type(token.DocumentHeaderType).Value("---").
			PositionLine(10).PositionColumn(1).PositionOffset(40).Build()
		doc3 := tkb.Clone().Type(token.StringType).Value("doc3").
			PositionLine(11).PositionColumn(1).PositionOffset(45).Build()

		input := token.Tokens{doc1, header2, doc2, header3, doc3}

		got := collectReset(tokens.SplitDocuments(input))

		require.Len(t, got, 3)

		// Doc 1 starts at line 1.
		assert.Equal(t, 1, got[0][0].Position.Line)

		// Doc 2 starts at line 1 (header) then line 2 (content).
		assert.Equal(t, 1, got[1][0].Position.Line)
		assert.Equal(t, 2, got[1][1].Position.Line)

		// Doc 3 starts at line 1 (header) then line 2 (content).
		assert.Equal(t, 1, got[2][0].Position.Line)
		assert.Equal(t, 2, got[2][1].Position.Line)
	})
}

// resetOne resets tks as a single document.
func resetOne(tks token.Tokens) token.Tokens {
	return tokens.ResetPositions(tks)
}

func TestResetPositions_Text(t *testing.T) {
	t.Parallel()

	t.Run("matches a fresh tokenize of the same text", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
		}{
			"after document header":        {input: "a: 1\n---\nb: 2\n"},
			"after document end":           {input: "a: 1\n...\nb: 2\n"},
			"comment after document end":   {input: "a: 1\n...\n# c\nb: 2\n"},
			"comment on document end line": {input: "a: 1\n... # c\nb: 2\n"},
			"indented after document end":  {input: "a: 1\n...\n  b: 2\n"},
			"header after document end":    {input: "a: 1\n...\n---\nb: 2\n"},
			"header after directive":       {input: "%YAML 1.2\n---\na: 1\n"},
			"leading blank lines":          {input: "\n\na: 1\n"},
			"crlf after document end":      {input: "a: 1\r\n...\r\nb: 2\r\n"},
			"bare cr after document end":   {input: "a: 1\r...\rb: 2\r"},
			"bare cr blank line":           {input: "a: 1\r...\r\rb: 2\r"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				for i, doc := range tokens.SplitDocuments(lexer.Tokenize(tc.input)) {
					doc = tokens.ResetPositions(doc)
					// A document's text is the Origins of its tokens.
					fresh := lexer.Tokenize(yamltest.DumpTokenOrigins(doc))
					require.Len(t, doc, len(fresh), "document %d", i)

					for j, want := range fresh {
						got := doc[j].Position
						assert.Equal(t, want.Position.Line, got.Line, "document %d token %d line", i, j)
						assert.Equal(t, want.Position.Column, got.Column, "document %d token %d column", i, j)
						assert.Equal(t, want.Position.Offset, got.Offset, "document %d token %d offset", i, j)
					}
				}
			})
		}
	})

	t.Run("counts whitespace tokens ahead of the text", func(t *testing.T) {
		t.Parallel()

		// The lexer emits an invalid token holding whitespace alone for a
		// tab that indents the line after "...", but a fresh tokenize of the
		// document's text emits none, so compare only the tokens with text.
		// The raw lexer miscounts the columns after such a tab, so
		// [tokens.Tokenize] gives the positions to compare against.
		withText := func(tks token.Tokens) token.Tokens {
			return slices.DeleteFunc(slices.Clone(tks), func(tk *token.Token) bool {
				return strings.Trim(tk.Origin, " \t\r\n") == ""
			})
		}

		tcs := map[string]struct {
			input string
		}{
			"tab before a key":          {input: "a: 1\n...\n\tb: 2\n"},
			"tab before a comment":      {input: "a: 1\n...\n\t# c\nb: 2\n"},
			"tab on a blank line":       {input: "a: 1\n...\n\t\nb: 2\n"},
			"tab after a crlf line end": {input: "a: 1\n...\r\n\tb: 2\r\n"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				for i, doc := range tokens.SplitDocuments(tokens.Tokenize(tc.input)) {
					doc = tokens.ResetPositions(doc)
					fresh := withText(tokens.Tokenize(yamltest.DumpTokenOrigins(doc)))
					text := withText(doc)
					require.Len(t, text, len(fresh), "document %d", i)

					for j, want := range fresh {
						got := text[j].Position
						assert.Equal(t, want.Position.Line, got.Line, "document %d token %d line", i, j)
						assert.Equal(t, want.Position.Column, got.Column, "document %d token %d column", i, j)
						assert.Equal(t, want.Position.Offset, got.Offset, "document %d token %d offset", i, j)
					}
				}
			})
		}
	})

	t.Run("ends a cut document where its text does", func(t *testing.T) {
		t.Parallel()

		// The lexer emits the content of a block scalar that the next
		// document's header closes before that header, so the content
		// ends the first document. No text follows it there, so it sits
		// at the end of the document's last line, as a fresh tokenize of
		// the document's text places it.
		tcs := map[string]struct {
			input string
			want  string
		}{
			"empty literal":                     {input: "a: |\n---\nb: 1\n", want: "1:5:5"},
			"empty stripped literal":            {input: "a: |-\n---\nb: 1\n", want: "1:6:6"},
			"nested empty literal":              {input: "x:\n  a: |\n---\nb: 1\n", want: "2:7:10"},
			"blank line":                        {input: "k: |\n\n---\nb: 1\n", want: "2:1:6"},
			"blank line with a tab":             {input: "a: |\n\t\n---\nb: 1\n", want: "2:2:7"},
			"empty literal in a sequence entry": {input: "- |\n---\n- b\n", want: "1:4:4"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				docs := collectReset(tokens.SplitDocuments(tokens.Tokenize(tc.input)))
				require.Len(t, docs, 2)

				doc := docs[0]
				last := doc[len(doc)-1]
				require.Empty(t, strings.Trim(last.Origin, " \t\r\n"))

				assert.Equal(t, tc.want, positionsOf(token.Tokens{last})[0])

				// The tokens with text keep the common shift.
				text := len(doc) - 1
				fresh := tokens.Tokenize(yamltest.DumpTokenOrigins(doc))
				require.GreaterOrEqual(t, len(fresh), text)
				assert.Equal(t, positionsOf(fresh)[:text], positionsOf(doc)[:text])
			})
		}
	})

	t.Run("keeps a trailing token of a whole stream in place", func(t *testing.T) {
		t.Parallel()

		// The lexer drops the "!", but the line holding it exists, and the
		// empty content of the block scalar stays on it.
		got := tokens.ResetPositions(tokens.Tokenize("k: |+\n!"))
		require.NotEmpty(t, got)

		assert.Equal(t, "2:1:7", positionsOf(got)[len(got)-1])
	})

	t.Run("anchors past an empty token", func(t *testing.T) {
		t.Parallel()

		// The empty content of a block scalar shares the position of the
		// text after it, but not the whitespace that opens that text's
		// Origin, so a stream cut at it anchors on the text.
		tcs := map[string]struct {
			input string
		}{
			"empty literal":          {input: "a: |\n\nb: 1\n"},
			"empty stripped literal": {input: "a: |-\n\nb: 1\n"},
			"empty folded":           {input: "a: >\n\n\nb: 1\n"},
			"indented key after it":  {input: "a:\n  k: |\n\n  b: 1\n"},
		}

		// ResetPositions leaves IndentNum and IndentLevel as they are, so
		// compare the line, column, and offset alone.
		lco := func(tk *token.Token) [3]int {
			return [3]int{tk.Position.Line, tk.Position.Column, tk.Position.Offset}
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				tks := tokens.Tokenize(tc.input)
				cut := slices.IndexFunc(tks, func(tk *token.Token) bool { return tk.Origin == "" })
				require.GreaterOrEqual(t, cut, 0)

				got := tokens.ResetPositions(tks[cut:])
				fresh := tokens.Tokenize(yamltest.DumpTokenOrigins(got))
				require.Len(t, got, len(fresh)+1)

				// The empty token shares the position of the token after it.
				assert.Equal(t, lco(got[1]), lco(got[0]))

				for j, want := range fresh {
					assert.Equal(t, lco(want), lco(got[j+1]), "token %d %q", j+1, got[j+1].Origin)
				}
			})
		}
	})

	t.Run("severs links to neighboring documents", func(t *testing.T) {
		t.Parallel()

		orig := lexer.Tokenize("a: 1\n...\nb: 2\n")
		docs := collectReset(tokens.SplitDocuments(orig))
		require.Len(t, docs, 2)

		first, second := docs[0], docs[1]

		assert.Nil(t, first[len(first)-1].Next, "the last clone of a document has no Next")
		assert.Nil(t, second[0].Prev, "the first clone of a document has no Prev")
		assert.Nil(t, first[0].Prev)
		assert.Nil(t, second[len(second)-1].Next)

		// The clones still link to each other inside a document.
		assert.Same(t, first[1], first[0].Next)
		assert.Same(t, first[0], first[1].Prev)

		// The originals keep their links across the boundary.
		assert.Same(t, orig[4], orig[3].Next)
		assert.Same(t, orig[3], orig[4].Prev)
	})

	t.Run("resets positions to line 1 column 1", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").
				PositionLine(5).PositionColumn(3).PositionOffset(100).Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").
				PositionLine(5).PositionColumn(6).PositionOffset(103).Build(),
			tkb.Clone().Type(token.StringType).Value("value").
				PositionLine(5).PositionColumn(8).PositionOffset(105).Build(),
		}

		got := resetOne(input)

		require.Len(t, got, 3)

		// First token should be at line 1, column 1, offset 1.
		assert.Equal(t, 1, got[0].Position.Line)
		assert.Equal(t, 1, got[0].Position.Column)
		assert.Equal(t, 1, got[0].Position.Offset)

		// Second token: same line, column adjusted relatively.
		assert.Equal(t, 1, got[1].Position.Line)
		assert.Equal(t, 4, got[1].Position.Column) // Column 6 relative to start 3, plus the 1-based origin.
		assert.Equal(t, 4, got[1].Position.Offset) // Offset 103 relative to start 100, plus the 1-based origin.

		// Third token: same line, column adjusted relatively.
		assert.Equal(t, 1, got[2].Position.Line)
		assert.Equal(t, 6, got[2].Position.Column) // Column 8 relative to start 3, plus the 1-based origin.
		assert.Equal(t, 6, got[2].Position.Offset) // Offset 105 relative to start 100, plus the 1-based origin.
	})

	t.Run("moves a stream holding whitespace alone to the start", func(t *testing.T) {
		t.Parallel()

		// A whitespace-only document cut after "..." lands at 1:1:1, where
		// [tokens.Tokenize] places the token it makes of the same text.
		tcs := map[string]struct {
			input string
			doc   int
			want  int
		}{
			"line break":          {input: "\n", want: 1},
			"two line breaks":     {input: "\n\n", want: 1},
			"spaces":              {input: "  ", want: 1},
			"spaces then a break": {input: "   \n", want: 1},
			"tab line between document end and header": {
				input: "a: 1\n...\n\t\n---\nb: 2\n",
				doc:   1,
				want:  1,
			},
			"tab line after document end at the end": {
				input: "a: 1\n...\n\t\n",
				doc:   1,
				want:  1,
			},
			"two tab lines between document end and header": {
				input: "a: 1\n...\n\t\n\t\n---\nb: 2\n",
				doc:   1,
				want:  2,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var got token.Tokens

				for i, doc := range tokens.SplitDocuments(tokens.Tokenize(tc.input)) {
					if i == tc.doc {
						got = resetOne(doc)
					}
				}

				require.Len(t, got, tc.want)

				for _, tk := range got {
					assert.Equal(t, 1, tk.Position.Line)
					assert.Equal(t, 1, tk.Position.Column)
					assert.Equal(t, 1, tk.Position.Offset)
				}
			})
		}
	})

	t.Run("handles multiline tokens", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").
				PositionLine(10).PositionColumn(5).PositionOffset(50).Build(),
			tkb.Clone().Type(token.StringType).Value("value").
				PositionLine(11).PositionColumn(5).PositionOffset(55).Build(),
		}

		got := resetOne(input)

		require.Len(t, got, 2)

		// First token at line 1.
		assert.Equal(t, 1, got[0].Position.Line)
		assert.Equal(t, 1, got[0].Position.Column)
		assert.Equal(t, 1, got[0].Position.Offset)

		// Second token at line 2 (relative).
		assert.Equal(t, 2, got[1].Position.Line)
		assert.Equal(t, 5, got[1].Position.Column) // Column preserved for non-first lines.
		assert.Equal(t, 6, got[1].Position.Offset) // Offset 55 relative to start 50, plus the 1-based origin.
	})

	t.Run("returns original slice for empty input", func(t *testing.T) {
		t.Parallel()

		input := token.Tokens{}
		got := resetOne(input)

		assert.Empty(t, got)
	})

	t.Run("returns original slice for nil input", func(t *testing.T) {
		t.Parallel()

		got := resetOne(nil)

		assert.Nil(t, got)
	})

	t.Run("clones tokens", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		original := tkb.Clone().Type(token.StringType).Value("key").
			PositionLine(5).PositionColumn(3).PositionOffset(100).Build()
		input := token.Tokens{original}

		got := resetOne(input)

		require.Len(t, got, 1)

		// Should be a different pointer.
		assert.NotSame(t, original, got[0])

		// Original should be unchanged.
		assert.Equal(t, 5, original.Position.Line)
		assert.Equal(t, 3, original.Position.Column)
		assert.Equal(t, 100, original.Position.Offset)
	})

	t.Run("handles nil position", func(t *testing.T) {
		t.Parallel()

		input := token.Tokens{
			&token.Token{Type: token.StringType, Value: "test", Position: nil},
		}

		got := resetOne(input)

		require.Len(t, got, 1)
		assert.Nil(t, got[0].Position)
	})

	t.Run("skips tokens with nil position when finding start", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			&token.Token{Type: token.StringType, Value: "nil-pos", Position: nil},
			tkb.Clone().Type(token.StringType).Value("key").
				PositionLine(5).PositionColumn(3).PositionOffset(100).Build(),
		}

		got := resetOne(input)

		require.Len(t, got, 2)

		// First token still has nil position.
		assert.Nil(t, got[0].Position)

		// ResetPositions moves the second token to line 1.
		assert.Equal(t, 1, got[1].Position.Line)
		assert.Equal(t, 1, got[1].Position.Column)
		assert.Equal(t, 1, got[1].Position.Offset)
	})

	t.Run("preserves token values", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("mykey").Origin("mykey").
				PositionLine(5).PositionColumn(3).Build(),
		}

		got := resetOne(input)

		require.Len(t, got, 1)
		assert.Equal(t, token.StringType, got[0].Type)
		assert.Equal(t, "mykey", got[0].Value)
		assert.Equal(t, "mykey", got[0].Origin)
	})
}

// positionsOf returns the position of each token as "line:column:offset".
func positionsOf(tks token.Tokens) []string {
	got := make([]string, 0, len(tks))
	for _, tk := range tks {
		got = append(got, fmt.Sprintf("%d:%d:%d", tk.Position.Line, tk.Position.Column, tk.Position.Offset))
	}

	return got
}

func TestTokenize_Positions(t *testing.T) {
	t.Parallel()

	// Every token sits on the rune where its text starts. The lexer alone
	// would count the trailing spaces it drops from a value. It would also
	// run one rune short after a comment or a tag, place a multi-line block
	// scalar on its last line, and count a CRLF it cuts between two tokens
	// twice.
	tcs := map[string]struct {
		input string
		want  []string
	}{
		"trailing spaces after a value": {
			input: "a: 1   \nb: 2\n",
			want:  []string{"1:1:1", "1:2:2", "1:4:4", "2:1:9", "2:2:10", "2:4:12"},
		},
		"header after a comment": {
			input: "# comment\n---\nb: two\n",
			want:  []string{"1:1:1", "2:1:11", "3:1:15", "3:2:16", "3:4:18"},
		},
		"value after a tag": {
			input: "t: !!str s\n...",
			want:  []string{"1:1:1", "1:2:2", "1:4:4", "1:10:10", "2:1:12"},
		},
		"block scalar at the end": {
			input: "k: |\n    hello\n    world\n",
			want:  []string{"1:1:1", "1:2:2", "1:4:4", "2:5:10"},
		},
		"block scalar with repeated lines": {
			input: "k: |\n  a\n  a\n",
			want:  []string{"1:1:1", "1:2:2", "1:4:4", "2:3:8"},
		},
		"block scalar followed by content": {
			input: "k: |\n  a\n  b\nz: 1\n",
			want:  []string{"1:1:1", "1:2:2", "1:4:4", "2:3:8", "4:1:14", "4:2:15", "4:4:17"},
		},
		"block scalar followed by a header": {
			input: "k: |\n  a\n---\nz: 1\n",
			want:  []string{"1:1:1", "1:2:2", "1:4:4", "2:3:8", "3:1:10", "4:1:14", "4:2:15", "4:4:17"},
		},
		"block scalar holding a comment and a key": {
			input: "k: |\n  a\n\n  # c\n  z: 1\n",
			want:  []string{"1:1:1", "1:2:2", "1:4:4", "2:3:8"},
		},
		"empty block scalar": {
			input: "a: |\nb: 1\n",
			want:  []string{"1:1:1", "1:2:2", "1:4:4", "2:1:6", "2:1:6", "2:2:7", "2:4:9"},
		},
		"comment in a CRLF file": {
			input: "key: value\r\n# c\r\nnext: 1\r\n",
			want:  []string{"1:1:1", "1:4:4", "1:6:6", "2:1:13", "3:1:18", "3:5:22", "3:7:24"},
		},
		"block scalar in a CRLF file": {
			input: "a: 1\r\nb: |\r\n  x\r\n  y\r\nc: 3\r\n",
			want: []string{
				"1:1:1",
				"1:2:2",
				"1:4:4",
				"2:1:7",
				"2:2:8",
				"2:4:10",
				"3:3:15",
				"5:1:23",
				"5:2:24",
				"5:4:26",
			},
		},
		"sequence after a tag and a blank line": {
			input: "a: !!seq\n\n  - b\n",
			want:  []string{"1:1:1", "1:2:2", "1:4:4", "3:3:13", "3:5:15"},
		},
		"tab between key and value": {
			input: "a:\t1\n",
			want:  []string{"1:1:1", "1:2:2", "1:4:4"},
		},
		"wide runes": {
			input: "a: 日本 x\nb: é\n",
			want:  []string{"1:1:1", "1:2:2", "1:4:4", "2:1:9", "2:2:10", "2:4:12"},
		},
		"key after a truncated escape": {
			// The lexer cuts "x41" out of the Origin, so the source does
			// not hold the scalar's text. Tokenize must not place the key
			// after it inside the scalar, where "c" also appears.
			input: "a: \"x\\x41 b c\"\nc: 2\n",
			want:  []string{"1:1:1", "1:2:2", "1:4:4", "2:1:16", "2:2:17", "2:4:19"},
		},
		"escape after a tag": {
			input: "name: !!str \"Caf\\u00e9\"\nage: 3\n",
			want:  []string{"1:1:1", "1:5:5", "1:7:7", "1:13:13", "2:1:25", "2:4:28", "2:6:30"},
		},
		"escape after a tag in a CRLF file": {
			input: "name: !!str \"Caf\\u00e9\"\r\nage: 3\r\n",
			want:  []string{"1:1:1", "1:5:5", "1:7:7", "1:13:13", "2:1:26", "2:4:29", "2:6:31"},
		},
		"escaped key after an escaped value": {
			input: "a: \"\\u00e9\"\n\"\\u00e8\": 1\n",
			want:  []string{"1:1:1", "1:2:2", "1:4:4", "2:1:13", "2:9:21", "2:11:23"},
		},
		"escape after a comment": {
			input: "m:\n  # c\n  \"\\u00e9\": 1\n  b: 2\n",
			want:  []string{"1:1:1", "1:2:2", "2:3:6", "3:3:12", "3:11:20", "3:13:22", "4:3:26", "4:4:27", "4:6:29"},
		},
		"tab indentation": {
			// The lexer drops the ":" after the tab, so the value's text
			// sits further on than the cursor.
			input: "\ta: 1\nb: 2\nc: 3\n",
			want:  []string{"1:2:2", "1:5:5", "2:2:8", "2:4:10", "3:1:12", "3:2:13", "3:4:15"},
		},
		"escape cut short by the end": {
			// The lexer reads the backslash into the invalid token for the
			// scalar and again into the token after it.
			input: "a: \"\\u: 1",
			want:  []string{"1:1:1", "1:2:2", "1:4:4", "1:5:5", "1:7:7", "1:9:9"},
		},
		"long escape cut short by the end": {
			input: "\"\\U: \\U",
			want:  []string{"1:1:1", "1:2:2", "1:4:4", "1:6:6"},
		},
		"open scalar cut at a header": {
			// The lexer drops "u00e9" and places the scalar at the header,
			// so the tokens after it must not land inside the escape.
			input: "a: \"caf\\u00e9\n---\nb: 1\n",
			want:  []string{"1:1:1", "1:2:2", "1:4:4", "2:1:15", "3:1:19", "3:2:20", "3:4:22"},
		},
		"unknown escape on a later line": {
			input: "name: \"caf\\u00e9\n  x \\d\"\nnext: 1\n",
			want:  []string{"1:1:1", "1:5:5", "1:7:7", "2:6:23", "3:1:26", "3:5:30", "3:7:32"},
		},
		"header after a cut escape": {
			// The lexer places the cut scalar at the fault, so its Offset
			// says nothing about where the tokens after it start.
			input: "a: \"\\x1\"\n---\nb: 1\n",
			want:  []string{"1:1:1", "1:2:2", "1:4:4", "2:1:10", "3:1:14", "3:2:15", "3:4:17"},
		},
		"comment after a header the lexer shortened": {
			// The lexer reads "---" as "--" one rune on, so the shift
			// taken from it misses the comment by one rune.
			input: "\"\t1\n---\n# c\n",
			want:  []string{"1:1:1", "2:1:5", "3:1:9"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, positionsOf(tokens.Tokenize(tc.input)))
		})
	}
}

// positionCorpus holds sources whose tokens exercise the positions the lexer
// places oddly.
var positionCorpus = map[string]string{
	"mapping":                                        "a: 1\nb: two\n",
	"trailing spaces":                                "a: 1   \nb: 2  \n",
	"trailing spaces before a comment":               "a: 1   # c\nb: 2\n",
	"comments":                                       "# head\na: 1 # line\n# foot\nb: 2\n",
	"header after a comment":                         "# comment\n---\nb: two\n",
	"tag on a value":                                 "t: !!str s\nu: !!int 1\n",
	"tag before a nested map":                        "a: !t\n  b: 1\nc: 2\n",
	"tag before a blank line":                        "a: !!seq\n\n  - b\n",
	"anchor and alias":                               "a: &x 1\nb: *x\n",
	"literal at the end":                             "k: |\n    hello\n    world\n",
	"literal with content after":                     "k: |\n  a\n  b\nz: 1\n",
	"literal with blank lines":                       "k: |\n  a\n\n  b\n\nz: 1\n",
	"literal with a blank line of spaces":            "k: |\n  a\n   \n  b\nz: 1\n",
	"literal with a leading blank line":              "k: |\n\n  b\nz: 1\n",
	"literal with keep":                              "k: |+\n  a\n\n\nz: 1\n",
	"literal with an indent indicator":               "k: |2\n   a\n  b\n",
	"literal with a comment after":                   "k: |\n  a\n# c\nz: 1\n",
	"literal followed by a header":                   "k: |\n  a\n---\nz: 1\n",
	"nested literal with a comment after":            "a:\n  k: |\n    x\n\n  # c\n  z: 1\n",
	"folded":                                         "k: >\n  a\n\n  b\n\nz: 1\n",
	"empty literal":                                  "a: |\nb: 1\n",
	"empty literal with keep":                        "a: |+\n\nb: 1\n",
	"literal of spaces before a comment":             "a: |+\n  \n# c\nb: 1\n",
	"folded of spaces before a comment":              "a: >+\n  \n# c\nb: 1\n",
	"literal of a tab before a comment":              "a: |\n \t\n# c\nb: 1\n",
	"literal of spaces after a header comment":       "a: |+ # h\n  \n# c\nb: 1\n",
	"nested literal of spaces before a comment":      "items:\n  - script: |+\n      \n    # next\n  - b\n",
	"literal of blank lines before a comment":        "a: |+\n  \n\n  \n# c\nb: 1\n",
	"crlf literal of spaces before a comment":        "a: |+\r\n  \r\n# c\r\nb: 1\r\n",
	"tagged literal of spaces before a comment":      "a: !t |+\n  \n# c\nb: 1\n",
	"empty folded before a comment":                  "x:\n  a: >\n  # c\n  b: 1\n",
	"empty folded before spaces and a comment":       "x:\n  a: >\n    \n  # c\n  b: 1\n",
	"quoted multi-line":                              "a: 'x\n\n  y'\nb: 1\n",
	"double-quoted multi-line":                       "c: \"x\n  y\"\nd: 1\n",
	"escape after a tag":                             "name: !!str \"Caf\\u00e9\"\nage: 3\n",
	"escape after a comment":                         "m:\n  # c\n  \"\\u00e9\": 1\n  b: 2\n",
	"escape in a flow sequence":                      "[!!str \"\\u00e9\", b]\n",
	"escape in a sequence":                           "- !!str \"\\u00e9\"\n- b\n",
	"escaped scalars in a row":                       "a: \"\\u00e9\"\n\"\\u00e8\": \"\\x41 b\"\nc: 1\n",
	"escape in a multi-line scalar":                  "a: \"x\\x41\n  b c\"\nc: 1\n",
	"escape that swallows the quote":                 "a: \"\\u12\"\nb: 1\nc: 2\n",
	"escape cut short by the end":                    "a: \"\\u: 1",
	"long escape cut short by the end":               "\"\\U: \\U",
	"high surrogate without a low one":               "a: \"\\uD83Dxx\"\nb: 1\n",
	"escape cut short on a later line":               "a: \"x\n  \\u1\"\n",
	"open scalar cut at a header":                    "a: \"caf\\u00e9\n---\nb: 1\n",
	"open scalar cut at a document end":              "a: \"x\\x41\n...\nb: 1\n",
	"open scalar cut after a code holding a rune":    "a: \"caf\\u00e9e\n---\nb: 1\n",
	"unknown escape after an escape":                 "name: \"caf\\u00e9\n  x \\d\"\nnext: 1\n",
	"header after a cut escape":                      "a: \"\\x1\"\n---\nb: 1\n",
	"comment after a shortened header":               "\"\t1\n---\n# c\n",
	"plain multi-line":                               "a: plain\n  multi\nb: 2\n",
	"flow collections":                               "{a: 1, b: [1, 2]}\n",
	"flow sequence over lines":                       "a: [\n  1,\n  2\n]\n",
	"sequences":                                      "- a\n- b # c\n- - c\n  - d\n",
	"complex key":                                    "? k\n: v\n",
	"directive":                                      "%YAML 1.2\n---\na: 1\n",
	"several documents":                              "a: 1\n---\nb: 2\n...\n# tail\nc: 3\n",
	"blank lines":                                    "a: 1\n\n\nb: 2\n",
	"blank line with a tab after a quote":            "a: \"t\tb\"\n \t\nb: 1\n",
	"key with trailing spaces":                       "a  : 1\n",
	"tab after a colon":                              "a:\t1\n",
	"wide runes":                                     "a: 日本 x\nb: é\n日: 1\n",
	"crlf":                                           "key: value\r\n# c\r\nnext: 1\r\n",
	"crlf literal":                                   "a: 1\r\nb: |\r\n  x\r\n  y\r\nc: 3\r\n",
	"crlf tag and quoted":                            "a: !t\r\n  b: 1\r\nc: 'x\r\n  y'\r\n",
	"bare cr":                                        "a: 1\rb: 2\r",
	"no final line ending":                           "a: 1\nb: 2",
	"space before a colon after quoted flow keys":    "{\"a\" : 1, \"b\" : \"x\"}\n",
	"space before a colon after an alias key":        "&a a : 1\n*a : 2\n",
	"space before a colon after a single-quoted key": "'a' : 1\n",
	"nested explicit key":                            "a:\n  ? zz\n  : x\n",
	"explicit key in a sequence entry":               "- ? earth\n  : blue\n",
	"explicit key after a document header":           "x: 1\n---\n\n\n? a\n: b\n",
	"explicit key after a flow sequence":             "tags: [a, b]\n\n? key\n: value\n",
	"explicit key after a comment":                   "# c\n\n? b\n: c\n",
	"explicit key after a blank line":                "\n? k\n: v\n",
	"crlf explicit key after a comment":              "# c\r\n\r\n? b\r\n",
	"trailing tab":                                   "a: 1\t\nb: 2\n",
	"trailing spaces before a key":                   "a: 1   \nb: 2\n",
	"trailing spaces after a quoted scalar":          "a: 'x'   \nb: 1\n",
	"trailing spaces after an anchor":                "a: &k   \n  b: 1\n",
	"trailing spaces after a tag":                    "a: !t   \n  b: 1\n",
	"trailing spaces after a flow sequence":          "a: [1, 2]   \nb: 2\n",
	"blank line of spaces":                           "a: 1\n   \nb: 2\n",
	"blank line of spaces after a document end":      "a: 1\n...\n  \nb: 2\n",
	"blank line of spaces in a plain scalar":         "a: plain\n   \n  multi\nb: 2\n",
	"colon after trailing spaces on a quoted key":    "\"q\" \n: 1\n",
	"colon after a trailing tab on a quoted key":     "\"q\"\t\n: 1\n",
	"colon after a blank line of spaces":             "\"q\"\n  \n: 1\n",
	"colon after trailing spaces crlf":               "'q' \r\n: 1\r\n",
	"nested colon after trailing spaces":             "- \"q\" \n  : 1\n",
	"colon after trailing spaces on a later key":     "a: 1\n'q'  \n: 2\n",
	"blank line of a tab before the first key":       "\t\nb: 1\n",
	"blank line with a tab before the first key":     " \t\nb: 1\n",
	"blank line of a tab before a quoted first key":  "\t\n\"b\": 1\n",
	"crlf blank line of a tab before the first key":  "\t\r\nb: 1\r\n",
	"bad block header at the end":                    "key: >foo",
	"bad block header and comment at the end":        "k: |ab # c",
	"bad block header in a later entry":              "- |ab\n- |ab",
	"text after a block scalar header at the end":    "key: |abc",
	"text after a block scalar header in a sequence": "- |ab\n- |cd",
}

func TestTokenize_PositionsLocateText(t *testing.T) {
	t.Parallel()

	for name, input := range positionCorpus {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			runes := []rune(input)

			for i, tk := range tokens.Tokenize(input) {
				require.NotNil(t, tk.Position, "token %d", i)

				at := tk.Position.Offset - 1
				require.GreaterOrEqual(t, at, 0, "token %d %q", i, tk.Origin)
				require.LessOrEqual(t, at, len(runes), "token %d %q", i, tk.Origin)

				// Line and Column follow from Offset: one more line per
				// line break before the rune, and the runes since the
				// last break.
				before := string(runes[:at])
				lastBreak := strings.LastIndexAny(before, "\r\n")

				assert.Equal(t, 1+countLineBreaks(before), tk.Position.Line, "token %d %q line", i, tk.Origin)

				wantCol := utf8.RuneCountInString(before[lastBreak+1:]) + 1
				assert.Equal(t, wantCol, tk.Position.Column, "token %d %q column", i, tk.Origin)

				// The first line of text in the Origin is in the source at
				// Offset. Tokenize restores the Origin of a double-quoted
				// scalar from the source, so all of its text is there.
				text := firstTextLine(tk.Origin)
				if tk.Type == token.DoubleQuoteType {
					text = strings.Trim(tk.Origin, " \t\r\n")
				}

				if text == "" {
					continue
				}

				assert.True(
					t,
					strings.HasPrefix(string(runes[at:]), text),
					"token %d %q at %d: %q",
					i,
					tk.Origin,
					at,
					string(runes[at:]),
				)
			}
		})
	}
}

// countLineBreaks counts "\r\n", "\n", and a bare "\r" as one break each.
func countLineBreaks(s string) int {
	return strings.Count(s, "\n") + strings.Count(s, "\r") - strings.Count(s, "\r\n")
}

// firstTextLine returns the first line of origin that holds text, without
// the whitespace around it, or "" when none does.
func firstTextLine(origin string) string {
	for _, ln := range strings.SplitAfter(strings.ReplaceAll(origin, "\r", "\n"), "\n") {
		if text := strings.Trim(ln, " \t\n"); text != "" {
			return text
		}
	}

	return ""
}

func TestTokenize_ParsesAsTheLexerDoes(t *testing.T) {
	t.Parallel()

	// The go-yaml parser reads Column and Line to decide which map or
	// sequence a token belongs to and where a comment attaches, so moving
	// tokens must not change the tree it builds from the stream ForParser
	// shapes.
	for name, input := range positionCorpus {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want, wantErr := parser.Parse(lexer.Tokenize(input), parser.ParseComments)
			got, gotErr := parser.Parse(tokens.ForParser(tokens.Tokenize(input)), parser.ParseComments)

			if wantErr != nil {
				require.Error(t, gotErr)

				return
			}

			require.NoError(t, gotErr)
			assert.Equal(t, nodeShape(want), nodeShape(got))

			// The rendering of a file spaces its nodes by their Lines, so
			// the blank lines the lexer's drift adds or drops are the
			// one difference the repair may make to it.
			assert.Equal(t, withoutBlankLines(want.String()), withoutBlankLines(got.String()))
		})
	}
}

// nodeShape lists every node of file in walk order: its type, its path,
// and the comments attached to it.
func nodeShape(file *ast.File) []string {
	var shape []string

	for _, doc := range file.Docs {
		ast.Walk(shapeVisitor(func(n ast.Node) {
			entry := n.Type().String() + " " + n.GetPath()
			if c := n.GetComment(); c != nil {
				entry += " comment=" + c.String()
			}

			var foot *ast.CommentGroupNode

			switch n := n.(type) {
			case *ast.MappingNode:
				foot = n.FootComment
			case *ast.MappingValueNode:
				foot = n.FootComment
			case *ast.SequenceNode:
				foot = n.FootComment
			}

			if foot != nil {
				entry += " foot=" + foot.String()
			}

			shape = append(shape, entry)
		}), doc)
	}

	return shape
}

// withoutBlankLines returns s with its blank lines removed.
func withoutBlankLines(s string) string {
	var kept []string

	for ln := range strings.SplitSeq(s, "\n") {
		if strings.TrimSpace(ln) != "" {
			kept = append(kept, ln)
		}
	}

	return strings.Join(kept, "\n")
}

// shapeVisitor is an [ast.Visitor] that calls itself on every node.
type shapeVisitor func(ast.Node)

func (v shapeVisitor) Visit(n ast.Node) ast.Visitor {
	v(n)

	return v
}
