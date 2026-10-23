package tokens_test

import (
	"cmp"
	"fmt"
	"iter"
	"maps"
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
	"go.jacobcolvin.com/niceyaml/line"
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

var (
	// Sources that both numericEscapes and positionCases hold.
	openScalarAtHeader = stringtest.LinesLF(
		`a: "caf\u00e9`,
		"---",
		"b: 1",
	)
	cutEscapeAtHeader = stringtest.LinesLF(
		`a: "\x1"`,
		"---",
		"b: 1",
	)
	unknownEscapeOnLaterLine = stringtest.LinesLF(
		`name: "caf\u00e9`,
		`  x \d"`,
		"next: 1",
	)

	// NumericEscapes holds sources with a "\x", "\u", or "\U" escape in a
	// double-quoted scalar.
	numericEscapes = map[string]string{
		"hex escape":            `a: "x\x41"` + "\n",
		"short unicode escape":  `a: "x\u0041"` + "\n",
		"long unicode escape":   `a: "x\U00000041"` + "\n",
		"text after the escape": `a: "x\x41 b"` + "\n",
		"flow mapping":          `m: {a: "\u00e9", b: xx}` + "\n",
		"trailing comment":      `a: "\u00e9" # c` + "\n",
		"multi-line scalar": stringtest.LinesLF(
			`a: "x\x41`,
			`  b c"`,
			"c: 1",
		),
		"surrogate pair":                  `a: "\uD83D\uDE00"` + "\n",
		"escaped quote before the escape": `a: "\"\u00e9"` + "\n",
		"hex escape over a quote":         `a: "\x"b"` + "\n",
		"escape the lexer keeps":          `a: "x\ty"` + "\n",
		"short unicode escape swallows the quote": stringtest.LinesLF(
			`a: "\u12"`,
			"b: 1",
			"c: 2",
			"d: 3",
		),
		"hex escape swallows the quote": stringtest.LinesLF(
			`a: "\x4"`,
			"b: 1",
		),
		"unicode escape one digit short": stringtest.LinesLF(
			`name: "caf\u00e"`,
			"age: 3",
		),
		"escape swallows the quote at the end": `a: "\x4"`,
		"escape swallows the quote and a cr": stringtest.LinesCRLF(
			`a: "\u12"`,
			"b: 1",
		),
		"escape swallows the quote before blank lines": stringtest.LinesLF(
			`a: "\u12"`,
			"b: 1",
			"  ",
			"",
		),
		"escape swallows the quote of the whole source": `"\x4"` + "\n",
		"unicode escape cut short by the end":           `a: "\u: 1`,
		"long unicode escape cut short by the end":      `"\U: \U`,
		"unicode escape cut short before the quote":     `a: "\u1"` + "\n",
		"high surrogate without a low one": stringtest.LinesLF(
			`a: "\uD83Dxx"`,
			"b: 1",
		),
		"escape cut short on a later line": stringtest.LinesLF(
			`a: "x`,
			`  \u1"`,
		),
		"escape cut short after an escaped backslash": `a: "\\\u"`,
		"open scalar cut at a header":                 openScalarAtHeader,
		"open scalar cut at a document end": stringtest.LinesLF(
			`a: "x\x41`,
			"...",
			"b: 1",
		),
		"open scalar cut at a header after an escape code that holds its next rune": stringtest.LinesLF(
			`a: "caf\u00e9e`,
			"---",
			"b: 1",
		),
		"open scalar cut at a header after a hex escape that holds its next rune": stringtest.LinesLF(
			`a: "x\x4ee`,
			"---",
			"b: 1",
		),
		"open scalar cut at a header after a surrogate pair": stringtest.LinesLF(
			`a: "\uD83D\uDE00D`,
			"---",
			"b: 1",
		),
		"open scalar cut at a header in a CRLF file": stringtest.LinesCRLF(
			`a: "caf\u00e9`,
			"---",
			"b: 1",
		),
		"open scalar cut at a header after a tag": stringtest.LinesLF(
			`a: !!str "caf\u00e9`,
			"---",
			"b: 1",
		),
		"open scalar cut at a header after trailing spaces": stringtest.LinesLF(
			`a: "caf\u00e9   `,
			"---",
			"b: 1",
		),
		"hex escape swallows the quote before a header": cutEscapeAtHeader,
		"hex escape swallows the quote before a document end": stringtest.LinesLF(
			`a: "\x1"`,
			"...",
			"b: 1",
		),
		"unicode escape swallows the quote and a cr before a header": stringtest.LinesCRLF(
			`a: "\u12"`,
			"---",
			"b: 1",
		),
		"hex escape swallows the quote and a space before a header": stringtest.LinesLF(
			`a: "\x" `,
			"---",
			"b: 1",
		),
		"unicode escape swallows a sequence entry before a header": stringtest.LinesLF(
			`"\u`,
			"- b",
			"---",
		),
		"escaped backslash before a header": stringtest.LinesLF(
			`a: "\\`,
			"---",
			"b: 1",
		),
		"escaped backslashes on two lines before a header": stringtest.LinesLF(
			`{a: "\\`,
			"\t"+`\\`,
			"---",
		),
		"escape cut short after a unicode escape":                       `a: "\u00e9\u"`,
		"long escape cut short after a hex escape and a tab":            `k: "\x41` + "\t" + `\U12"` + "\n",
		"escape cut short after an escaped backslash before hex digits": `k: "\x41\\x41\u"` + "\n",
		"high surrogate without a low one after a tab": stringtest.LinesLF(
			`- ":`+"\t\t"+`\N\uD83D\u`+"\t"+`"`,
			"b: 1",
		),
		"long escape that takes a header before a second header": stringtest.LinesLF(
			`k: "\U12\"`,
			"---",
			"",
			"---",
			"",
			"b: 1",
		),
		"unknown escape after an escape": `a: "caf\u00e9 \d"` + "\n",
		"unknown escape on a later line": unknownEscapeOnLaterLine,
		"unknown escape after a comment": stringtest.LinesLF(
			"a: # c",
			`  "caf\u00e9 \d"`,
			"b: 1",
		),
		"escape cut short after an escape": `a: "caf\u00e9 \u1"` + "\n",
	}

	// DroppedLineBreaks holds sources with line breaks the lexer drops. The want
	// field holds the joined Origins of a source where they differ from it,
	// around the invalid token the lexer makes for a tab on a blank line.
	droppedLineBreaks = map[string]struct {
		input string
		want  string
	}{
		"key after a flow sequence": {
			input: stringtest.LinesLF(
				"tags: [a, b]",
				"",
				"? key",
				": value",
			),
		},
		"value after a flow mapping": {
			input: stringtest.LinesLF(
				"a: {x: y}",
				"",
				": v",
			),
		},
		"key after a quoted scalar": {
			input: stringtest.LinesLF(
				"a: 'x'",
				"",
				"? b",
				": c",
			),
		},
		"key after an alias": {
			input: stringtest.LinesLF(
				"a: *x",
				"",
				"? b",
			),
		},
		"key after an empty value": {
			input: stringtest.LinesLF(
				"a:",
				"",
				"? b",
				": c",
			),
		},
		"key after a document header": {
			input: stringtest.LinesLF(
				"---",
				"",
				"? b",
			),
		},
		"key after a line comment": {
			input: stringtest.LinesLF(
				"a: 1 # note",
				"",
				"? k",
			),
		},
		"key after a comment": {
			input: stringtest.LinesLF(
				"# c",
				"",
				"? k",
			),
		},
		"key after a blank line": {
			input: stringtest.LinesLF(
				"",
				"? k",
				": v",
			),
		},
		"key after blank lines": {
			input: stringtest.LinesLF(
				"",
				"",
				"? b",
				": c",
			),
		},
		"nested key after a flow sequence": {
			input: stringtest.LinesLF(
				"a:",
				"  b: [x]",
				"",
				"  ? k",
				"  : v",
			),
		},
		"crlf key after a comment": {
			input: stringtest.LinesCRLF(
				"# c",
				"",
				"? k",
			),
		},
		"plain scalar after a blank line of spaces": {
			input: stringtest.LinesLF(
				"t: \"a\tb\"",
				"  ",
				"  r'",
				"- e",
			),
		},
		"plain scalar after blank lines of spaces": {
			input: stringtest.LinesLF(
				"t: \"a\tb\"",
				"  ",
				"  ",
				"  r'",
				"- e",
			),
		},
		"key after blank lines of spaces": {
			input: stringtest.LinesLF(
				"a: \"t\tb\"",
				"  ",
				"  ",
				"c: 1",
			),
		},
		"comment after blank lines of spaces": {
			input: stringtest.LinesLF(
				"a: \"t\tb\"",
				"  ",
				"  ",
				"# c",
				"d: 1",
				"e: 2",
			),
		},
		"crlf key after a blank line the lexer rewrites": {
			// The lexer turns the CRLF in front of the invalid blank line
			// into "\n".
			input: stringtest.LinesCRLF(
				"x:",
				"  a: \"t\tb\"",
				" \t",
				"  ",
				"? b",
			),
			want: stringtest.LinesLF(
				"x:\r",
				"  a: \"t\tb\"",
				" \t\r",
				"\r",
				"? b\r",
			),
		},
		"key after blank lines of spaces after bare crs": {
			input: stringtest.LinesLF(
				"k: \"\t\"\r ",
				"\r ",
				"c: 1",
			),
		},
		"document header after a cut scalar and bare crs": {input: `- "\u\q` + "\r\r \n---\n"},
		"key after a blank line and blank lines of spaces after bare crs": {
			// The lexer reads each "\r", space, and "\n" as one CRLF, so
			// the breaks Tokenize puts back end with a bare "\r" in front
			// of the "\n" that opens the Origin.
			input: stringtest.LinesLF(
				"",
				"\r ",
				"\r ",
				"key: 1",
			),
		},
		"document end after blank lines of spaces after bare crs": {
			input: stringtest.LinesLF(
				"k:",
				"\r ",
				"\r ",
				"...",
			),
		},
		"key after blank lines of spaces after a comment that ends with a bare cr": {
			// The comment's Origin ends with a bare "\r", so a "\n" that
			// opens the breaks Tokenize puts back would join it into one
			// CRLF.
			input: stringtest.JoinLF(
				"a: 1 # c\r ",
				"\r ",
				"b: 2",
			),
		},
		"scalar after blank lines of spaces after a bare cr comment": {
			input: stringtest.JoinLF(
				"#\r ",
				"\r ",
				"0",
			),
		},
		"key after a bare cr and a line of spaces between tab lines of a quoted scalar": {
			// The lexer reads the "\r", the space, and the "\n" as one
			// CRLF in a token of whitespace alone, so the key's Origin
			// takes the line break the stream lacks.
			input: stringtest.LinesLF(
				`a: "x`,
				"\t\r ",
				"\t",
				"",
				"b: 1",
			),
			want: stringtest.LinesLF(
				`a: "x`,
				"\t\r",
				"\t",
				"",
				"",
				"b: 1",
			),
		},
		"closing quote after a bare cr and a line of spaces between tab lines": {
			input: stringtest.LinesLF(
				`a: "x`,
				"\t\r ",
				"\t\"",
				"b: 1",
			),
			want: stringtest.LinesLF(
				`a: "x`,
				"\t\r",
				"\t",
				"\t\"",
				"b: 1",
			),
		},
		"key after a bare cr and a line of spaces between tab lines of plain text": {
			input: stringtest.LinesLF(
				"a: x",
				"\t\r ",
				"\t",
				"",
				"b: 1",
			),
			want: stringtest.LinesLF(
				"a: x",
				"\t\r",
				"\t",
				"",
				"",
				"b: 1",
			),
		},
		"value after a comment that ends with a crlf the lexer cuts": {input: "#\r\n:"},
		"value after a line of spaces after a bare cr comment":       {input: "#\r \n:"},
		"key after a blank line with a tab": {
			// The blank line goes to the end of the scalar's Origin. A
			// tab in front of a line break in the key's Origin would
			// make the parser reject the key.
			input: stringtest.LinesLF(
				"a: \"t\tb\"",
				" \t",
				"b: 1",
			),
		},
		"key after trailing spaces the lexer drops": {
			// The lexer drops the spaces after the flow mapping and makes
			// an invalid token of the blank line, whose indentation must
			// not repeat. Tokenize leaves the whitespace around that
			// token as the lexer made it.
			input: stringtest.LinesLF(
				"{a: b}  ",
				" \t",
				"? k",
			),
			want: stringtest.LinesLF(
				"{a: b}",
				" \t",
				"? k",
			),
		},
	}

	// DroppedWhitespace holds sources with whitespace the lexer drops. The want
	// field holds the joined Origins of a source where they differ from it.
	droppedWhitespace = map[string]struct {
		input string
		want  string
	}{
		"space before a colon after quoted flow keys":    {input: `{"a" : 1, "b" : "x"}` + "\n"},
		"space before a colon after an alias key":        {input: "&a a : 1\n*a : 2\n"},
		"space before a colon after a single-quoted key": {input: "'a' : 1\n"},
		"nested explicit key": {
			input: stringtest.LinesLF(
				"a:",
				"  ? zz",
				"  : x",
			),
		},
		"explicit key in a sequence entry": {
			input: stringtest.LinesLF(
				"- ? earth",
				"  : blue",
			),
		},
		"explicit key after a document header": {
			input: stringtest.LinesLF(
				"x: 1",
				"---",
				"",
				"",
				"? a",
				": b",
			),
		},
		"explicit key after a comment": {
			input: stringtest.LinesLF(
				"# c",
				"",
				"? b",
				": c",
			),
		},
		"crlf explicit key after a comment": {
			input: stringtest.LinesCRLF(
				"# c",
				"",
				"? b",
			),
		},
		"trailing tab":                          {input: "a: 1\t\nb: 2\n"},
		"trailing spaces after a value":         {input: "a: 1   \nb: 2\n"},
		"trailing spaces after a quoted scalar": {input: "a: 'x'   \nb: 1\n"},
		"trailing spaces after an anchor": {
			input: stringtest.LinesLF(
				"a: &k   ",
				"  b: 1",
			),
		},
		"trailing spaces after a tag": {
			input: stringtest.LinesLF(
				"a: !t   ",
				"  b: 1",
			),
		},
		"trailing spaces after a flow sequence": {input: "a: [1, 2]   \nb: 2\n"},
		"blank line of spaces": {
			input: stringtest.LinesLF(
				"a: 1",
				"   ",
				"b: 2",
			),
		},
		"blank line of spaces after a document end": {
			input: stringtest.LinesLF(
				"a: 1",
				"...",
				"  ",
				"b: 2",
			),
		},
		"blank line of spaces in a plain scalar": {
			input: stringtest.LinesLF(
				"a: plain",
				"   ",
				"  multi",
				"b: 2",
			),
		},
		"blank line with a tab before the first key": {input: " \t\na: 1\n"},
		"blank line of spaces after a bare cr":       {input: "\r \na: 1\n"},
		"blank lines of spaces after a bare cr in a mapping": {
			input: stringtest.LinesLF(
				"a:\r  \r ",
				"  b: 2",
			),
		},
		"line ending the lexer repeats after a tag": {
			// The source holds one line break where the Origins hold two,
			// so Tokenize keeps the Origins the lexer made.
			input: stringtest.LinesLF(
				"u: !t",
				"  v: 1",
			),
			want: stringtest.LinesLF(
				"u: !t",
				"",
				"  v: 1",
			),
		},
		"blank line after a line ending the lexer repeats": {
			// Tokenize keeps the Origins the lexer made around the
			// repeat, so the blank line loses its spaces.
			input: stringtest.LinesLF(
				"a: !t",
				"  ",
				"  b: 1",
			),
			want: stringtest.LinesLF(
				"a: !t",
				"",
				"",
				"  b: 1",
			),
		},
		"line ending the lexer repeats after a block scalar header": {
			// The lexer makes an invalid token of the header and the
			// text after it, and repeats the line ending that closes it.
			input: stringtest.LinesLF(
				"a: | x",
				"  b",
			),
			want: stringtest.LinesLF(
				"a: | x",
				"",
				"  b",
			),
		},
		"bad block header at the end": {
			// The lexer repeats the header's last rune as a token of its
			// own when no line ending follows, and Tokenize drops it.
			input: "key: >foo",
		},
		"bad block header and comment at the end": {input: "k: |ab # c"},
		"bad block header in a later entry": {
			// The line ending after the first header repeats as well.
			input: "- |ab\n- |ab",
			want: stringtest.JoinLF(
				"- |ab",
				"",
				"- |ab",
			),
		},
		"text after a block scalar header at the end": {
			// The lexer reads the last rune of the text a second time,
			// and Tokenize drops the token it makes of it.
			input: "key: |abc",
		},
		"text after a folded scalar header at the end": {input: "a: >x y"},
		"lone tag marker at the end": {
			// The lexer drops the "!" and the spaces in front of it.
			input: "a: !",
			want:  "a:",
		},
		"lone tag marker on the last line": {
			input: "a: b\n!",
			want:  "a: b\n",
		},
		"named tag at the end": {
			input: "a: !t",
			want:  "a:",
		},
		"secondary tag at the end": {
			input: "a: !!str",
			want:  "a:",
		},
		"named tag on the last line": {
			// The line break in front of the tag goes with it.
			input: "a:\n!t",
			want:  "a:",
		},
		"named tag on the line after a plain scalar": {
			// The scalar in front of the tag keeps the line break.
			input: "a: b\n!t",
			want:  "a: b\n",
		},
		"tag holding a flow end at the end": {
			// Only a space, a line break, or a "," in a flow collection
			// closes a tag.
			input: "[!t]",
			want:  "[",
		},
		"tag holding a comma at the end": {
			input: "a: !t,",
			want:  "a:",
		},
		"tag before a comma in a flow collection": {input: "[!t,"},
		"tag holding a brace at the end": {
			// The lexer keeps the tag's text as an invalid token.
			input: "a: !t}",
		},
		"named tag before a trailing space":            {input: "a: !t "},
		"alias after a block scalar header at the end": {input: "|*x"},
		"bad block header ending in a double quote": {
			// The lexer makes an invalid token of the repeated quote.
			input: `key: >"quoted"`,
		},
		"bad block header ending in a single quote":    {input: "key: |'x'"},
		"bad block header ending in an at sign":        {input: "k: |a@"},
		"bad block header ending in a backtick":        {input: "k: |a`"},
		"bad header ending in a quote after a comment": {input: "# c\nb: |ab'"},
		"bad block header holding a quoted word":       {input: `key: |a"b"`},
		"reserved indicators in a row at the end": {
			// Two invalid tokens that follow no block scalar header both
			// hold text of the source.
			input: ": @@",
		},
		"reserved indicators in a row in a sequence entry": {input: "- @@"},
		"backticks in a row at the end":                    {input: "key: ``"},
		"bad tags in a row after a document end":           {input: "...!}!}"},
		"bad tags in a row after a document header":        {input: "--- !}!}"},
		"bad tags in a row after a flow sequence":          {input: "[{:[]!}!}"},
	}

	// PositionCases holds sources and the position of each of their tokens.
	positionCases = map[string]struct {
		input string
		want  []string
	}{
		"trailing spaces after a value": {
			input: stringtest.LinesLF(
				"a: 1   ",
				"b: 2",
			),
			want: []string{"1:1:1", "1:2:2", "1:4:4", "2:1:9", "2:2:10", "2:4:12"},
		},
		"header after a comment": {
			input: stringtest.LinesLF(
				"# comment",
				"---",
				"b: two",
			),
			want: []string{"1:1:1", "2:1:11", "3:1:15", "3:2:16", "3:4:18"},
		},
		"value after a tag": {
			input: stringtest.JoinLF(
				"t: !!str s",
				"...",
			),
			want: []string{"1:1:1", "1:2:2", "1:4:4", "1:10:10", "2:1:12"},
		},
		"value after a tag before a truncated last token": {
			// The lexer places a token one rune short after a tag. When
			// the last token's text is not in the source, every position
			// must still move to where the source holds the text.
			input: stringtest.LinesLF(
				"a: !!str x",
				`name: "Caf\u00e9"`,
			),
			want: []string{"1:1:1", "1:2:2", "1:4:4", "1:10:10", "2:1:12", "2:5:16", "2:7:18"},
		},
		"block scalar at the end": {
			input: stringtest.LinesLF(
				"k: |",
				"    hello",
				"    world",
			),
			want: []string{"1:1:1", "1:2:2", "1:4:4", "2:5:10"},
		},
		"block scalar with repeated lines": {
			input: stringtest.LinesLF(
				"k: |",
				"  a",
				"  a",
			),
			want: []string{"1:1:1", "1:2:2", "1:4:4", "2:3:8"},
		},
		"block scalar followed by content": {
			input: stringtest.LinesLF(
				"k: |",
				"  a",
				"  b",
				"z: 1",
			),
			want: []string{"1:1:1", "1:2:2", "1:4:4", "2:3:8", "4:1:14", "4:2:15", "4:4:17"},
		},
		"block scalar followed by a header": {
			input: stringtest.LinesLF(
				"k: |",
				"  a",
				"---",
				"z: 1",
			),
			want: []string{"1:1:1", "1:2:2", "1:4:4", "2:3:8", "3:1:10", "4:1:14", "4:2:15", "4:4:17"},
		},
		"block scalar holding a comment and a key": {
			input: stringtest.LinesLF(
				"k: |",
				"  a",
				"",
				"  # c",
				"  z: 1",
			),
			want: []string{"1:1:1", "1:2:2", "1:4:4", "2:3:8"},
		},
		"empty block scalar": {
			input: stringtest.LinesLF(
				"a: |",
				"b: 1",
			),
			want: []string{"1:1:1", "1:2:2", "1:4:4", "2:1:6", "2:1:6", "2:2:7", "2:4:9"},
		},
		"comment in a CRLF file": {
			input: stringtest.LinesCRLF(
				"key: value",
				"# c",
				"next: 1",
			),
			want: []string{"1:1:1", "1:4:4", "1:6:6", "2:1:13", "3:1:18", "3:5:22", "3:7:24"},
		},
		"block scalar in a CRLF file": {
			input: stringtest.LinesCRLF(
				"a: 1",
				"b: |",
				"  x",
				"  y",
				"c: 3",
			),
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
			input: stringtest.LinesLF(
				"a: !!seq",
				"",
				"  - b",
			),
			want: []string{"1:1:1", "1:2:2", "1:4:4", "3:3:13", "3:5:15"},
		},
		"tab between key and value": {
			input: "a:\t1\n",
			want:  []string{"1:1:1", "1:2:2", "1:4:4"},
		},
		"wide runes": {
			input: stringtest.LinesLF(
				"a: 日本 x",
				"b: é",
			),
			want: []string{"1:1:1", "1:2:2", "1:4:4", "2:1:9", "2:2:10", "2:4:12"},
		},
		"key after a truncated escape": {
			// The lexer cuts "x41" out of the Origin, so the source does
			// not hold the scalar's text. Tokenize must not place the key
			// after it inside the scalar, where "c" also appears.
			input: stringtest.LinesLF(
				`a: "x\x41 b c"`,
				"c: 2",
			),
			want: []string{"1:1:1", "1:2:2", "1:4:4", "2:1:16", "2:2:17", "2:4:19"},
		},
		"escape after a tag": {
			input: stringtest.LinesLF(
				`name: !!str "Caf\u00e9"`,
				"age: 3",
			),
			want: []string{"1:1:1", "1:5:5", "1:7:7", "1:13:13", "2:1:25", "2:4:28", "2:6:30"},
		},
		"escape after a tag in a CRLF file": {
			input: stringtest.LinesCRLF(
				`name: !!str "Caf\u00e9"`,
				"age: 3",
			),
			want: []string{"1:1:1", "1:5:5", "1:7:7", "1:13:13", "2:1:26", "2:4:29", "2:6:31"},
		},
		"escaped key after an escaped value": {
			input: stringtest.LinesLF(
				`a: "\u00e9"`,
				`"\u00e8": 1`,
			),
			want: []string{"1:1:1", "1:2:2", "1:4:4", "2:1:13", "2:9:21", "2:11:23"},
		},
		"escape after a comment": {
			input: stringtest.LinesLF(
				"m:",
				"  # c",
				`  "\u00e9": 1`,
				"  b: 2",
			),
			want: []string{"1:1:1", "1:2:2", "2:3:6", "3:3:12", "3:11:20", "3:13:22", "4:3:26", "4:4:27", "4:6:29"},
		},
		"tab indentation": {
			// The lexer drops the ":" after the tab, so the value's text
			// sits further on than the cursor.
			input: stringtest.LinesLF(
				"\ta: 1",
				"b: 2",
				"c: 3",
			),
			want: []string{"1:2:2", "1:5:5", "2:2:8", "2:4:10", "3:1:12", "3:2:13", "3:4:15"},
		},
		"unicode escape cut short by the end": {
			// The lexer reads the backslash into the invalid token for the
			// scalar and again into the token after it.
			input: `a: "\u: 1`,
			want:  []string{"1:1:1", "1:2:2", "1:4:4", "1:5:5", "1:7:7", "1:9:9"},
		},
		"long unicode escape cut short by the end": {
			input: `"\U: \U`,
			want:  []string{"1:1:1", "1:2:2", "1:4:4", "1:6:6"},
		},
		"open scalar cut at a header": {
			// The lexer drops "u00e9" and places the scalar at the header,
			// so the tokens after it must not land inside the escape.
			input: openScalarAtHeader,
			want:  []string{"1:1:1", "1:2:2", "1:4:4", "2:1:15", "3:1:19", "3:2:20", "3:4:22"},
		},
		"unknown escape on a later line": {
			input: unknownEscapeOnLaterLine,
			want:  []string{"1:1:1", "1:5:5", "1:7:7", "2:6:23", "3:1:26", "3:5:30", "3:7:32"},
		},
		"hex escape swallows the quote before a header": {
			// The lexer places the cut scalar at the fault, so its Offset
			// says nothing about where the tokens after it start.
			input: cutEscapeAtHeader,
			want:  []string{"1:1:1", "1:2:2", "1:4:4", "2:1:10", "3:1:14", "3:2:15", "3:4:17"},
		},
		"comment after a header the lexer shortened": {
			// The lexer reads "---" as "--" one rune on, so the shift
			// taken from it misses the comment by one rune.
			input: stringtest.LinesLF(
				"\"\t1",
				"---",
				"# c",
			),
			want: []string{"1:1:1", "2:1:5", "3:1:9"},
		},
		"escaped line break before a tab line that opens with a backslash": {
			input: stringtest.JoinLF(
				`b: "\`,
				"\t\t\\",
			),
			want: []string{"1:1:1", "1:2:2", "1:4:4", "2:3:9"},
		},
		"text on the line after a tab line that swallows its indicator": {
			// The token the lexer makes of the second tab swallows the ":"
			// that ends the line, and the tokens of whitespace alone sit on
			// that ":". The text of the next token opens the line after it.
			input: stringtest.JoinLF(
				`a: "x`,
				"\t\t:",
				"\t,,",
				"\t",
			),
			want: []string{"1:1:1", "1:2:2", "1:4:4", "2:3:9", "2:3:9", "3:2:12"},
		},
		"quote on the line after a tab line that swallows its indicator": {
			input: stringtest.JoinLF(
				`a: "x`,
				"\t\t-",
				"\t\"\r",
				"\t",
			),
			want: []string{"1:1:1", "1:2:2", "1:4:4", "2:3:9", "2:3:9", "3:2:12", "4:2:16"},
		},
	}

	// PositionCorpus holds sources whose tokens exercise the positions the lexer
	// places oddly. The tests of those positions read [positionSources], which
	// adds the sources of [positionCases], [droppedWhitespace],
	// [droppedLineBreaks], and [numericEscapes].
	positionCorpus = map[string]string{
		"mapping":                          "a: 1\nb: two\n",
		"trailing spaces on each line":     "a: 1   \nb: 2  \n",
		"trailing spaces before a comment": "a: 1   # c\nb: 2\n",
		"comments": stringtest.LinesLF(
			"# head",
			"a: 1 # line",
			"# foot",
			"b: 2",
		),
		"tag on a value": "t: !!str s\nu: !!int 1\n",
		"tag before a nested map": stringtest.LinesLF(
			"a: !t",
			"  b: 1",
			"c: 2",
		),
		"anchor and alias": "a: &x 1\nb: *x\n",
		"literal with blank lines": stringtest.LinesLF(
			"k: |",
			"  a",
			"",
			"  b",
			"",
			"z: 1",
		),
		"literal with a blank line of spaces": stringtest.LinesLF(
			"k: |",
			"  a",
			"   ",
			"  b",
			"z: 1",
		),
		"literal with a leading blank line": stringtest.LinesLF(
			"k: |",
			"",
			"  b",
			"z: 1",
		),
		"literal with keep": stringtest.LinesLF(
			"k: |+",
			"  a",
			"",
			"",
			"z: 1",
		),
		"literal with an indent indicator": stringtest.LinesLF(
			"k: |2",
			"   a",
			"  b",
		),
		"literal with a comment after": stringtest.LinesLF(
			"k: |",
			"  a",
			"# c",
			"z: 1",
		),
		"nested literal with a comment after": stringtest.LinesLF(
			"a:",
			"  k: |",
			"    x",
			"",
			"  # c",
			"  z: 1",
		),
		"folded": stringtest.LinesLF(
			"k: >",
			"  a",
			"",
			"  b",
			"",
			"z: 1",
		),
		"empty literal with keep": stringtest.LinesLF(
			"a: |+",
			"",
			"b: 1",
		),
		"literal of spaces before a comment": stringtest.LinesLF(
			"a: |+",
			"  ",
			"# c",
			"b: 1",
		),
		"folded of spaces before a comment": stringtest.LinesLF(
			"a: >+",
			"  ",
			"# c",
			"b: 1",
		),
		"literal of a tab before a comment": stringtest.LinesLF(
			"a: |",
			" \t",
			"# c",
			"b: 1",
		),
		"literal of spaces after a header comment": stringtest.LinesLF(
			"a: |+ # h",
			"  ",
			"# c",
			"b: 1",
		),
		"nested literal of spaces before a comment": stringtest.LinesLF(
			"items:",
			"  - script: |+",
			"      ",
			"    # next",
			"  - b",
		),
		"literal of blank lines before a comment": stringtest.LinesLF(
			"a: |+",
			"  ",
			"",
			"  ",
			"# c",
			"b: 1",
		),
		"crlf literal of spaces before a comment": stringtest.LinesCRLF(
			"a: |+",
			"  ",
			"# c",
			"b: 1",
		),
		"tagged literal of spaces before a comment": stringtest.LinesLF(
			"a: !t |+",
			"  ",
			"# c",
			"b: 1",
		),
		"empty folded before a comment": stringtest.LinesLF(
			"x:",
			"  a: >",
			"  # c",
			"  b: 1",
		),
		"empty folded before spaces and a comment": stringtest.LinesLF(
			"x:",
			"  a: >",
			"    ",
			"  # c",
			"  b: 1",
		),
		"quoted multi-line": stringtest.LinesLF(
			"a: 'x",
			"",
			"  y'",
			"b: 1",
		),
		"double-quoted multi-line": stringtest.LinesLF(
			`c: "x`,
			`  y"`,
			"d: 1",
		),
		"escape in a flow sequence": `[!!str "\u00e9", b]` + "\n",
		"escape in a sequence": stringtest.LinesLF(
			`- !!str "\u00e9"`,
			"- b",
		),
		"escaped scalars in a row": stringtest.LinesLF(
			`a: "\u00e9"`,
			`"\u00e8": "\x41 b"`,
			"c: 1",
		),
		"escape that swallows the quote": stringtest.LinesLF(
			`a: "\u12"`,
			"b: 1",
			"c: 2",
		),
		"plain multi-line": stringtest.LinesLF(
			"a: plain",
			"  multi",
			"b: 2",
		),
		"flow collections": "{a: 1, b: [1, 2]}\n",
		"flow sequence over lines": stringtest.LinesLF(
			"a: [",
			"  1,",
			"  2",
			"]",
		),
		"sequences": stringtest.LinesLF(
			"- a",
			"- b # c",
			"- - c",
			"  - d",
		),
		"complex key": "? k\n: v\n",
		"directive": stringtest.LinesLF(
			"%YAML 1.2",
			"---",
			"a: 1",
		),
		"several documents": stringtest.LinesLF(
			"a: 1",
			"---",
			"b: 2",
			"...",
			"# tail",
			"c: 3",
		),
		"blank lines": stringtest.LinesLF(
			"a: 1",
			"",
			"",
			"b: 2",
		),
		"key with trailing spaces": "a  : 1\n",
		"wide runes in a key": stringtest.LinesLF(
			"a: 日本 x",
			"b: é",
			"日: 1",
		),
		"crlf tag and quoted": stringtest.LinesCRLF(
			"a: !t",
			"  b: 1",
			"c: 'x",
			"  y'",
		),
		"bare cr":              "a: 1\rb: 2\r",
		"no final line ending": "a: 1\nb: 2",
		"colon after trailing spaces on a quoted key": stringtest.LinesLF(
			`"q" `,
			": 1",
		),
		"colon after a trailing tab on a quoted key": stringtest.LinesLF(
			`"q"`+"\t",
			": 1",
		),
		"colon after a blank line of spaces": stringtest.LinesLF(
			`"q"`,
			"  ",
			": 1",
		),
		"colon after trailing spaces crlf": stringtest.LinesCRLF(
			"'q' ",
			": 1",
		),
		"nested colon after trailing spaces": stringtest.LinesLF(
			`- "q" `,
			"  : 1",
		),
		"colon after trailing spaces on a later key": stringtest.LinesLF(
			"a: 1",
			"'q'  ",
			": 2",
		),
		"blank line of a tab before the first key":             "\t\nb: 1\n",
		"blank line of a space and a tab before the first key": " \t\nb: 1\n",
		"blank line of a tab before a quoted first key":        "\t\n\"b\": 1\n",
		"crlf blank line of a tab before the first key":        "\t\r\nb: 1\r\n",
		"text after a block scalar header in a sequence":       "- |ab\n- |cd",
	}
)

func TestTokenize_TabIndentsQuotedLine(t *testing.T) {
	t.Parallel()

	// When a tab indents a later line of a quoted scalar, the lexer ends
	// the scalar's invalid token with the tab and reads the tab again as a
	// token of its own. Tokenize drops that repeat and gives the scalar the
	// whitespace the lexer drops after the tab, so the joined Origins equal
	// the input, and each token on the tab's line starts past the one
	// before it. The tokens on both sides of a repeat link to each other.
	// A backslash that ends the scalar's text stays with it when the tab's
	// line opens with one. The want field holds the joined Origins where
	// they differ from the input. The lexer drops a lone "!". It also
	// drops the code of a "\x" or "\u" escape, which Tokenize does not
	// restore in a scalar the lexer cut at a tab.
	tcs := map[string]struct {
		input string
		want  string
	}{
		"double-quoted": {
			input: stringtest.LinesLF(
				`a: "x`,
				"\ty\"",
			),
		},
		"single-quoted": {
			input: stringtest.LinesLF(
				"a: 'x",
				"\ty'",
			),
		},
		"scalars in a row": {
			input: stringtest.LinesLF(
				`a: "x`,
				"\ty\"",
				"b: 'x",
				"\ty'",
				`c: "x`,
				"\ty\"",
			),
		},
		"sequence entry": {
			input: stringtest.LinesLF(
				`- "x`,
				"\ty\"",
			),
		},
		"nested sequence entry": {
			input: stringtest.LinesLF(
				"a:",
				`  - "x`,
				"\ty\"",
			),
		},
		"two tabs": {
			input: stringtest.LinesLF(
				`a: "x`,
				"\t\ty\"",
			),
		},
		"space before the tab": {
			input: stringtest.LinesLF(
				"a:",
				`  b: "x`,
				" \ty\"",
			),
		},
		"after a blank line": {
			input: stringtest.LinesLF(
				"a: 'x",
				"",
				"\ty'",
			),
		},
		"crlf": {
			input: stringtest.LinesCRLF(
				`a: "x`,
				"\ty\"",
			),
		},
		"no final line ending": {
			input: stringtest.JoinLF(
				"0:",
				`"`,
				"\t0",
			),
		},
		"escape code": {
			input: stringtest.LinesLF(
				`a: "caf\u00e9`,
				"\t",
				`  more"`,
				"b: 1",
			),
			want: stringtest.LinesLF(
				`a: "caf\`,
				"\t",
				`  more"`,
				"b: 1",
			),
		},
		"escape code and two lines": {
			input: stringtest.LinesLF(
				`a: "caf\u00e9`,
				"\t",
				"\t",
				`  more"`,
				"b: 1",
			),
			want: stringtest.LinesLF(
				`a: "caf\`,
				"\t",
				"\t",
				`  more"`,
				"b: 1",
			),
		},
		"tab line at the end": {input: stringtest.LinesLF(`a: "x`, "\t")},
		"tab lines at the end": {
			input: stringtest.LinesLF(
				`a: "x`,
				"\t",
				"\t",
			),
		},
		"tab that ends the source": {input: stringtest.JoinLF(`a: "x`, "\t")},
		"dropped text on the tab line": {
			input: stringtest.JoinLF(
				`a: "x`,
				"\t!",
			),
			want: stringtest.JoinLF(`a: "x`, "\t"),
		},
		"dropped text before a bare cr on the tab line": {
			input: stringtest.JoinLF(
				"a: \"\t\t\t]",
				"\t?\r\u00e9>",
				"\t",
			),
			want: stringtest.JoinLF(
				"a: \"\t\t\t]",
				"\t\u00e9>",
				"\t",
			),
		},
		"two tabs on a tab line": {
			input: stringtest.LinesLF(
				`a: "x`,
				"\t\t",
				"\ty\"",
			),
		},
		"two tabs on a single-quoted tab line": {
			input: stringtest.LinesLF(
				"- '",
				"\t\t",
				"\ta'",
			),
		},
		"space after the tab on a tab line": {
			input: stringtest.LinesLF(
				`a: "x`,
				"\t ",
				"\ty\"",
			),
		},
		"two tabs on a crlf tab line": {
			input: stringtest.LinesCRLF(
				`a: "x`,
				"\t\t",
				"\ty\"",
			),
		},
		"three tabs in a nested map": {
			input: stringtest.LinesLF(
				"a:",
				`  b: "x`,
				"\t\t\ty\"",
			),
		},
		"tab lines of two tabs that end the source": {
			input: stringtest.JoinLF(
				`a: "x`,
				"\t\t",
				"\t",
				"\t",
			),
		},
		"tab in the first line of a nested scalar": {
			input: stringtest.LinesLF(
				"a:",
				"  b: \"\tx",
				"\t\t\ty\"",
			),
		},
		"hex escape before a backslash": {
			input: stringtest.LinesLF(
				`a: "c\x41`,
				"\t\\x42\"",
			),
			want: stringtest.LinesLF(
				`a: "c\`,
				"\t\\x42\"",
			),
		},
		"escaped line break before a backslash": {
			input: stringtest.LinesLF(
				`a: "x\`,
				"\t\\y\"",
			),
		},
		"escaped line break before two tabs": {
			input: stringtest.JoinLF(
				`b: "\`,
				"\t\t\\",
			),
		},
		"hex escapes on tab lines of two tabs": {
			input: stringtest.JoinLF(
				`:: "`,
				`\x4}`,
				"\t\t\\x4|",
				"\t\t",
			),
			want: stringtest.JoinLF(
				`:: "`,
				`\`,
				"\t\t\\x4|",
				"\t\t",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := tokens.Tokenize(tc.input)
			assert.Equal(t, cmp.Or(tc.want, tc.input), yamltest.DumpTokenOrigins(tks))
			require.NoError(t, yamltest.ValidateLines(line.NewLines(tks)))

			require.NoError(t, yamltest.ValidateTokenLinks(tks))
		})
	}
}

func TestTokenize_TabAfterBlockTextThatOpensWithQuote(t *testing.T) {
	t.Parallel()

	// When a tab indents the line after text that follows a block scalar
	// header and opens with a quote, the lexer makes an invalid token of
	// the text that looks like a quoted scalar it cut at a tab. The lexer
	// repeats no tab after that token, so Tokenize drops no token. The
	// joined Origins equal the input, and each token on a line starts past
	// the one before it.
	tcs := map[string]struct {
		input string
	}{
		"unicode escape": {
			input: stringtest.LinesLF(
				"a: |",
				`  "caf\u00e9`,
				"\tsome text \\u00e9",
			),
		},
		"hex escape": {
			input: stringtest.JoinLF(
				"a: |",
				`  "\x`,
				"\tx",
			),
		},
		"unicode escape with space": {
			input: stringtest.JoinLF(
				"|",
				` "\u`,
				"\t \\u",
			),
		},
		"after a blank line": {
			input: stringtest.JoinLF(
				"- |",
				"",
				`"\x`,
				"\tx",
			),
		},
		"folded": {
			input: stringtest.LinesLF(
				"a: >",
				`  "\x41`,
				"\t\tx41",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := tokens.Tokenize(tc.input)
			assert.Equal(t, tc.input, yamltest.DumpTokenOrigins(tks))
			require.NoError(t, yamltest.ValidateLines(line.NewLines(tks)))
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
	for name, input := range numericEscapes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := yamltest.DumpTokenOrigins(tokens.Tokenize(input))
			assert.Equal(t, input, got)
		})
	}
}

func TestTokenize_FinalBlankLines(t *testing.T) {
	t.Parallel()

	// The stream keeps the blank lines that end the file, even when the
	// lexer rewrote the last token, cut it short, or gave it whitespace
	// alone. The whitespace comes back once, so a block scalar header that
	// holds the start of it does not repeat it. A line that ends in spaces
	// the lexer dropped does not cost the file its final lines, and neither
	// does a blank line of spaces. A last token that sits in front of text
	// the lexer dropped keeps the line ending it holds.
	tcs := map[string]struct {
		input string
		// The end of the input's final whitespace that the joined Origins
		// hold, when the lexer drops spaces ahead of it. Empty means all
		// of it.
		tail string
		// The joined Origins equal the input.
		whole bool
	}{
		"trailing space inside the last scalar": {
			input: stringtest.LinesLF(
				"description: first line ",
				"  second line",
				"",
			),
		},
		"blank line of spaces inside the last scalar": {
			input: stringtest.LinesLF(
				"description: first",
				"  ",
				"  second",
				"",
			),
		},
		"several final blank lines": {
			input: stringtest.LinesLF(
				"k: plain",
				"   ",
				"  more",
				"",
				"",
			),
		},
		"sequence entry": {
			input: stringtest.LinesLF(
				"- a",
				"  ",
				"  b",
				"",
			),
		},
		"invalid tab token": {
			input: stringtest.LinesLF(
				"a:",
				"\t",
				"",
			),
			whole: true,
		},
		"invalid tab token after a header": {
			input: stringtest.LinesLF(
				"a: |",
				"\t",
				"",
			),
			whole: true,
		},
		"invalid tab token after a trailing space": {
			input: stringtest.LinesLF(
				"a:",
				"  b: 1 ",
				"\t\t",
				"",
			),
			tail: "\n\t\t\n\n",
		},
		"invalid tab token after a trailing space crlf": {
			input: "  b: 2 \r\n\t\t\n",
			tail:  "\r\n\t\t\n",
		},
		"invalid tab token after a blank line of spaces": {
			input: stringtest.LinesLF(
				"a:",
				"  b: 1",
				"  ",
				"\t\t",
			),
			tail: "\n\t\t\n",
		},
		"invalid tab token of a space and a tab after a trailing space": {
			input: stringtest.LinesLF(
				"a:",
				"  b: 1 ",
				"\t \t",
			),
			tail: "\n\t \t\n",
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
			input: stringtest.LinesLF(
				"a: |  ",
				"  ",
				" ",
			),
			whole: true,
		},
		"dropped text ends the file": {input: "a: 1\n!"},
		"tab indentation": {
			// The lexer swallows characters after a tab used as
			// indentation, so the joined Origins are not a prefix of the
			// source.
			input: "\ta: 1\n",
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
			if tc.tail != "" {
				require.True(t, strings.HasSuffix(tail, tc.tail), "input tail %q", tail)

				tail = tc.tail
			}

			assert.True(t, strings.HasSuffix(got, tail), "joined origins %q", got)

			if tc.whole {
				assert.Equal(t, tc.input, got)
			}
		})
	}
}

func TestTokenize_FinalTabToken(t *testing.T) {
	t.Parallel()

	// The last token of each source is the invalid token the lexer makes
	// of a tab, and it keeps the Origin the lexer gave it. When a tab used
	// as indentation opens the file, that token can swallow the rest of the
	// text, and the whitespace after the swallowed text follows it. In a
	// longer source, the whitespace of the tokens before it can match a
	// later part of the source's final whitespace once the match skips
	// spaces and tabs. Its own whitespace does not follow there, so it
	// keeps the tab it holds.
	tcs := map[string]struct {
		input string
		want  string // The Origin of the last token.
		// The token is the only one of the stream.
		alone bool
	}{
		"opening tab swallows a sequence entry":                 {input: "\t-", want: "\t", alone: true},
		"opening tab swallows a mapping value":                  {input: "\t:", want: "\t", alone: true},
		"opening tab swallows an entry before a line ending":    {input: "\t-\n", want: "\t\n", alone: true},
		"opening tab swallows an entry before a blank line":     {input: "\t-\n\n", want: "\t\n\n", alone: true},
		"opening tab swallows an entry before a trailing space": {input: "\t- ", want: "\t ", alone: true},
		"opening tab after a space swallows an entry":           {input: " \t-", want: " \t", alone: true},
		"two opening tabs swallow an entry before a crlf":       {input: "\t\t-\r\n", want: "\t\t\r\n", alone: true},
		"swallowed sequence entry before a space": {
			input: stringtest.LinesLF(
				"# c",
				"\t- ",
			),
			want: "\t",
		},
		"swallowed mapping value before a space": {
			input: stringtest.LinesLF(
				"# c",
				"\t: ",
			),
			want: "\t",
		},
		"swallowed sequence entry before a tab": {
			input: stringtest.LinesLF(
				"# c",
				"\t-\t",
			),
			want: "\t",
		},
		"swallowed sequence entry between bare crs": {input: "# c\r\t- \r", want: "\t"},
		"swallowed mapping values in a nested mapping": {
			input: stringtest.JoinLF(
				"a:",
				"  b: 1:\t:\t: \t",
			),
			want: "\t",
		},
		"line ending the lexer repeats after a tag": {
			input: stringtest.LinesLF(
				"a: !t",
				"\t",
				"\t",
			),
			want: "\n\t",
		},
		"line ending the lexer repeats after header text": {
			input: stringtest.LinesLF(
				`a: |"x`,
				"\t",
				"\t",
			),
			want: "\n\t",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := tokens.Tokenize(tc.input)
			require.NotEmpty(t, tks)

			if tc.alone {
				require.Len(t, tks, 1)
			}

			last := tks[len(tks)-1]
			assert.Equal(t, token.InvalidType, last.Type)
			assert.Equal(t, tc.want, last.Origin)
		})
	}
}

func TestTokenize_ByteOrderMark(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		// The joined Origins, where they differ from the input.
		want string
		// The place of the key b, as yamltest.TokenPlace gives it, when the
		// input holds one.
		place string
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
			input: stringtest.LinesLF(
				"a: 1",
				"---",
				"\ufeffb: 2",
			),
			want: stringtest.LinesLF(
				"a: 1",
				"---",
				"b: 2",
			),
			place: "b@3:1",
		},
		"after a document marker crlf": {
			input: stringtest.LinesCRLF(
				"a: 1",
				"---",
				"\ufeffb: 2",
			),
			want: stringtest.LinesCRLF(
				"a: 1",
				"---",
				"b: 2",
			),
			place: "b@3:1",
		},
		"after a document marker cr": {
			input: "a: 1\r---\r\ufeffb: 2\r",
			want:  "a: 1\r---\rb: 2\r",
			place: "b@3:1",
		},
		"after a document marker and a comment": {
			input: stringtest.LinesLF(
				"a: 1",
				"--- # c",
				"# d",
				"\ufeffb: 2",
			),
			want: stringtest.LinesLF(
				"a: 1",
				"--- # c",
				"# d",
				"b: 2",
			),
			place: "b@4:1",
		},
		"before a document marker": {
			input: stringtest.LinesLF(
				"\ufeff---",
				"a: 1",
				"\ufeff---",
				"\ufeffb: 2",
			),
			want: stringtest.LinesLF(
				"---",
				"a: 1",
				"---",
				"b: 2",
			),
			place: "b@4:1",
		},
		"after a document end marker": {
			input: stringtest.LinesLF(
				"a: 1",
				"...",
				"\ufeffb: 2",
			),
			want: stringtest.LinesLF(
				"a: 1",
				"...",
				"b: 2",
			),
			place: "b@3:1",
		},
		"after a document end marker cr": {
			input: "a: 1\r...\r\ufeffb: 2\r",
			want:  "a: 1\r...\rb: 2\r",
			place: "b@3:1",
		},
		"inside a document":               {input: "a: 1\n\ufeffb: 2\n"},
		"inside a document mixed endings": {input: "# c\ra: 1\n\ufeffb: 2\n"},
		"after a marker with content":     {input: "--- a\n\ufeffb\n"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := tokens.Tokenize(tc.input)
			assert.Equal(t, cmp.Or(tc.want, tc.input), yamltest.DumpTokenOrigins(tks))

			if tc.place == "" {
				return
			}

			idx := slices.IndexFunc(tks, func(tk *token.Token) bool { return tk.Value == "b" })
			require.GreaterOrEqual(t, idx, 0)
			assert.Equal(t, tc.place, yamltest.TokenPlace(tks[idx]))
		})
	}
}

func TestTokenize_OpenFlowAtMarker(t *testing.T) {
	t.Parallel()

	// The lexer keeps its counts of open flow collections across a marker.
	// On its own, it reads the documents after a "{" or "[" that nothing
	// closes as the inside of a flow collection, and misses the flow
	// sequence after a "]" that closes nothing. Each head ends in such a
	// document. The tokens from the marker that opens tail on are those of
	// tail as a source of its own, moved to where tail sits in the input.
	// The tail of many cases is markerAndComma. Its comma reads as a flow
	// indicator while the lexer counts a flow as open.
	markerAndComma := stringtest.LinesLF("---", "k: v, w")

	tcs := map[string]struct {
		head string
		tail string
		// The values of the tokens of tail, for a case that lists them.
		want []string
	}{
		"comma and nested mapping after an open mapping": {
			head: stringtest.LinesLF(
				"name: web",
				"labels: {app: web",
			),
			tail: stringtest.LinesLF(
				"---",
				"name: worker",
				"command: sleep 1, 2",
				"spec:",
				"  replicas: 2",
			),
			want: []string{
				"---",
				"name", ":", "worker",
				"command", ":", "sleep 1, 2",
				"spec", ":", "replicas", ":", "2",
			},
		},
		"comma after an open sequence": {
			head: "a: [b, c\n",
			tail: markerAndComma,
			want: []string{"---", "k", ":", "v, w"},
		},
		"flow sequence after a sequence end that closes nothing": {
			head: "a: ]\n",
			tail: stringtest.LinesLF("---", "x: [a, b]"),
			want: []string{"---", "x", ":", "[", "a", ",", "b", "]"},
		},
		"document end marker": {
			head: "a: [b\n",
			tail: stringtest.LinesLF(
				"...",
				"k:",
				"  r: 2",
				"m: x, y",
			),
			want: []string{"...", "k", ":", "r", ":", "2", "m", ":", "x, y"},
		},
		"content on the line of the marker": {
			head: "a: [b\n",
			tail: "--- v, w\n",
			want: []string{"---", "v, w"},
		},
		"crlf": {
			head: "a: {b\r\n",
			tail: stringtest.LinesCRLF(
				"---",
				"k:",
				"  r: 2",
				"m: x, y",
			),
			want: []string{"---", "k", ":", "r", ":", "2", "m", ":", "x, y"},
		},
		// The lexer counts a CRLF after a comment as two lines, so its
		// own Line for the marker is two lines late here.
		"crlf after comments": {
			head: stringtest.LinesCRLF(
				"a: [b # one",
				"# two",
			),
			tail: stringtest.LinesCRLF("---", "k: v, w"),
			want: []string{"---", "k", ":", "v, w"},
		},
		"bare cr": {
			head: "a: {b\r",
			tail: "---\rk: v, w\r",
			want: []string{"---", "k", ":", "v, w"},
		},
		"text of several bytes ahead of the marker": {
			head: "é: [ü,\n",
			tail: markerAndComma,
			want: []string{"---", "k", ":", "v, w"},
		},
		"blank lines ahead of the marker": {
			head: stringtest.LinesLF(
				"a: {b",
				"",
				"  ",
			),
			tail: markerAndComma,
			want: []string{"---", "k", ":", "v, w"},
		},
		"unclosed quote inside the flow": {
			head: `a: ["b` + "\n",
			tail: markerAndComma,
			want: []string{"---", "k", ":", "v, w"},
		},
		"each of several documents leaves a flow open": {
			head: "a: {\n",
			tail: stringtest.LinesLF(
				"---",
				"b: [",
				"---",
				"k: v, w",
				"---",
				"z: ]",
				"---",
				"l: [a, b]",
			),
			want: []string{
				"---", "b", ":", "[",
				"---", "k", ":", "v, w",
				"---", "z", ":", "]",
				"---", "l", ":", "[", "a", ",", "b", "]",
			},
		},
		// The second open flow sits past the first window of lines that
		// Tokenize lexes from the first marker.
		"open flows far apart": {
			head: "a: {b\n",
			tail: "---\n" +
				strings.Repeat("- item, more\n", 20) +
				stringtest.LinesLF("c: [", "---") +
				strings.Repeat("k: v, w\n", 20),
		},
		// The windows end inside the block scalar and the quoted scalar.
		"scalars of many lines between open flows": {
			head: "a: [b\n",
			tail: stringtest.LinesLF("---", "s: |") +
				strings.Repeat("  text, more\n", 20) +
				`q: "` + strings.Repeat("text, more\n  ", 20) + stringtest.LinesLF(`"`, "c: {") +
				markerAndComma,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			input := tc.head + tc.tail

			got := tokens.Tokenize(input)
			assert.Equal(t, input, yamltest.DumpTokenOrigins(got))

			want := tokens.Tokenize(tc.tail)
			require.Greater(t, len(got), len(want))

			lines, runes := countLineBreaks(tc.head), utf8.RuneCountInString(tc.head)
			for _, tk := range want {
				tk.Position.Line += lines
				tk.Position.Offset += runes
			}

			tail := got[len(got)-len(want):]

			// The marker keeps the whitespace the stream holds in front
			// of it, such as the line break after a "{".
			assert.Equal(t, want[0].Origin, strings.TrimLeft(tail[0].Origin, " \t\r\n"))

			want[0].Origin = tail[0].Origin

			yamltest.RequireTokensEqual(t, want, tail)

			if tc.want != nil {
				assert.Equal(t, tc.want, yamltest.TokenValues(tail))
			}

			// The tokens on both sides of the marker link to each other.
			require.NoError(t, yamltest.ValidateTokenLinks(got))
		})
	}
}

func TestTokenize_OpenFlowBehindSwallowedText(t *testing.T) {
	t.Parallel()

	// A tab in front of a ":" makes the lexer swallow the ":", so the
	// source holds text between the "{" and the marker that no token
	// holds. Tokenize leaves the stream as the lexer made it there, and
	// the "," of the next document still reads as a flow indicator.
	input := stringtest.LinesLF(
		"{\t: ",
		"---",
		"k: v, w",
	)

	want := lexer.Tokenize(input)
	got := tokens.Tokenize(input)

	require.Len(t, got, len(want))

	for i, tk := range got {
		assert.Equal(t, want[i].Type, tk.Type, "token %d %q", i, tk.Origin)
		assert.Equal(t, want[i].Value, tk.Value, "token %d %q", i, tk.Origin)
	}

	assert.Equal(t, token.CollectEntryType, got[len(got)-2].Type)
}

func TestIsPlaceholder(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		idx   int
		want  bool
	}{
		"whitespace only":             {input: "\n  \n", want: true},
		"plain scalar":                {input: "abc", want: false},
		"plain scalar line":           {input: "abc\n", want: false},
		"mapping":                     {input: "a: 1\n", want: false},
		"empty block scalar content":  {input: "a: |\nb: 1\n", idx: 3, want: false},
		"empty folded scalar content": {input: "a: >\nb: 1\n", idx: 3, want: false},
		"kept blank block scalar lines": {
			input: stringtest.LinesLF(
				"a: |+",
				"",
				"",
				"b: 1",
			),
			idx:  3,
			want: false,
		},
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
		input string
		want  string // Position of the token, as positionsOf gives it.
	}{
		"header ends the file":      {input: "a: |+\n", want: "1:6:6"},
		"header ends the file crlf": {input: "a: |+\r\n", want: "1:6:6"},
		"header ends the file cr":   {input: "a: |+\r", want: "1:6:6"},
		"sequence entry":            {input: "- >+\n", want: "1:5:5"},
		"second header ends it":     {input: stringtest.LinesLF(`x: "é"`, "k: |+"), want: "2:6:13"},
		"dropped line follows":      {input: "a: |+\n!", want: "2:1:7"},
		"blank line ends the file":  {input: "a: |+\n\n", want: "2:1:7"},
		"blank lines end the file": {
			input: stringtest.LinesLF(
				"a: |+",
				"",
				"",
			),
			want: "3:1:8",
		},
		"blank crlf line ends it":   {input: "a: |+\r\n\r\n", want: "2:1:8"},
		"spaces end the file":       {input: "a: >+\n  \n", want: "2:3:9"},
		"clipped blank line ends":   {input: "a: |\n\n", want: "2:1:6"},
		"key follows":               {input: "a: |+\nb: 1\n", want: "2:1:7"},
		"header after a cut escape": {input: `"\u>+: |` + "\n\n", want: "2:1:10"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := tokens.Tokenize(tc.input)
			empty := slices.IndexFunc(tks, blankOrigin)
			require.GreaterOrEqual(t, empty, 0)

			assert.Equal(t, tc.want, positionsOf(tks)[empty])
		})
	}
}

func TestTokenize_PlaceholderPosition(t *testing.T) {
	t.Parallel()

	// The placeholder sits where its text starts, like any other token,
	// and at 1:1:1 when the source holds whitespace alone. A fresh stream
	// already counts from 1, so ResetPositions keeps its position.
	tcs := map[string]struct {
		input string
		want  string // Position of the token, as positionsOf gives it.
	}{
		"lone tag marker":   {input: "!", want: "1:1:1"},
		"spaces before":     {input: "  !", want: "1:3:3"},
		"tab before":        {input: "\t!", want: "1:2:2"},
		"line break before": {input: "\n!", want: "2:1:2"},
		"indented after a break": {
			input: stringtest.JoinLF(
				"",
				"  !",
			),
			want: "2:3:4",
		},
		"indented after two breaks": {
			input: stringtest.JoinLF(
				"",
				"",
				"  !",
			),
			want: "3:3:5",
		},
		"tab after two breaks": {
			input: stringtest.JoinLF(
				"",
				"",
				"\t!",
			),
			want: "3:2:4",
		},
		"whitespace alone":           {input: "  \n", want: "1:1:1"},
		"line breaks alone":          {input: "\n\n", want: "1:1:1"},
		"byte order mark is dropped": {input: "\ufeff  !", want: "1:3:3"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := tokens.Tokenize(tc.input)
			require.Len(t, tks, 1)
			require.True(t, tokens.IsPlaceholder(tks[0]))

			assert.Equal(t, []string{tc.want}, positionsOf(tks))

			reset := tokens.ResetPositions(tks)
			require.Len(t, reset, 1)
			assert.Equal(t, *tks[0].Position, *reset[0].Position)
		})
	}
}

func TestTokenize_RestoresDroppedLineBreaks(t *testing.T) {
	t.Parallel()

	// The lexer drops the line breaks and indentation between some tokens
	// and a "?" or ":" indicator that follows them, and a blank line from
	// a run of them after a double-quoted scalar that holds a tab.
	// Tokenize puts them back, so the Origins ahead of each token's text
	// hold as many line breaks as the source does. The sources also go
	// through TestTokenize_PositionsLocateText, which checks the rune that
	// each position names.
	for name, tc := range droppedLineBreaks {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var joined strings.Builder

			for i, tk := range tokens.Tokenize(tc.input) {
				require.NotNil(t, tk.Position, "token %d", i)

				if firstTextLine(tk.Origin) != "" {
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
				}

				joined.WriteString(tk.Origin)
			}

			assert.Equal(t, cmp.Or(tc.want, tc.input), joined.String())
		})
	}
}

func TestTokenize(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
	}{
		"empty string":     {input: ""},
		"simple key value": {input: "key: value\n"},
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
		"unicode content":          {input: "greeting: こんにちは\n"},
		"whitespace only":          {input: "\n\n"},
		"spaces and tabs only":     {input: " \t \n"},
		"trailing blank line":      {input: "key: value\n\n"},
		"crlf trailing blank line": {input: "key: value\r\n\r\n"},
		"trailing blank line of spaces": {
			input: stringtest.LinesLF(
				"key: value",
				"   ",
				"",
			),
		},
		"trailing line of spaces": {input: "key: value\n  \n"},
		// The lexer drops a tag that holds the whole source, and the
		// placeholder token holds it.
		"lone tag marker": {input: "!"},
		"named tag alone": {input: "!t"},
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
			assert.Equal(t, tc.input, yamltest.DumpTokenOrigins(got))

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
	// input.
	for name, tc := range droppedWhitespace {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, cmp.Or(tc.want, tc.input), yamltest.DumpTokenOrigins(tokens.Tokenize(tc.input)))
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
		want  string // The joined Origins, where they differ from the input.
	}{
		"blank line of a tab": {
			input: "\t\nb: 1\n",
			want:  "\nb: 1\n",
		},
		"indented key after a blank line with a tab": {
			input: stringtest.LinesLF(
				" \t ",
				"  b: 1",
			),
			want: stringtest.LinesLF(
				"",
				"  b: 1",
			),
		},
		"crlf blank lines": {
			input: stringtest.LinesCRLF(
				"\t",
				" ",
				"b: 1",
			),
			want: stringtest.LinesCRLF(
				"",
				"",
				"b: 1",
			),
		},
		"comment after a blank line of a tab": {
			input: stringtest.LinesLF(
				"\t",
				"# c",
				"b: 1",
			),
			want: stringtest.LinesLF(
				"",
				"# c",
				"b: 1",
			),
		},
		"blank line of spaces after the first key": {
			input: stringtest.LinesLF(
				"a: 1",
				"  ",
				"b: 2",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := tokens.Tokenize(tc.input)
			got := tokens.ForParser(tks)

			assert.Equal(t, cmp.Or(tc.want, tc.input), yamltest.DumpTokenOrigins(got))
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
	docs := collectDocs(tokens.SplitDocuments(tokens.Tokenize(stringtest.LinesLF(
		"a: 1",
		"---",
		"b: 2",
	))))
	require.Len(t, docs, 2)
	require.NotNil(t, docs[1][0].Prev)

	got := tokens.ForParser(append(token.Tokens{nil}, docs[1]...))
	require.Len(t, got, len(docs[1]))

	require.NoError(t, yamltest.ValidateTokenLinks(got))
}

func TestSplitDocuments(t *testing.T) {
	t.Parallel()

	tkb := yamltest.NewTokenBuilder()
	str := func(value string) *token.Token {
		return tkb.Clone().Type(token.StringType).Value(value).Build()
	}
	colon := func() *token.Token {
		return tkb.Clone().Type(token.MappingValueType).Value(":").Build()
	}
	header := func() *token.Token {
		return tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
	}
	end := func() *token.Token {
		return tkb.Clone().Type(token.DocumentEndType).Value("...").Build()
	}

	// RequireDocs requires each of docs to hold the next tokens of input,
	// as many as want gives for it, and returns the tokens of input after
	// them.
	requireDocs := func(t *testing.T, input token.Tokens, want []int, docs []token.Tokens) token.Tokens {
		t.Helper()

		require.Len(t, docs, len(want))

		for i, n := range want {
			yamltest.RequireTokensEqual(t, input[:n], docs[i])

			input = input[n:]
		}

		return input
	}

	tcs := map[string]struct {
		input token.Tokens
		want  []int // Number of tokens in each document.
	}{
		"nil input":   {input: nil},
		"empty slice": {input: token.Tokens{}},
		"single doc no header": {
			input: token.Tokens{str("key"), colon(), str("value")},
			want:  []int{3},
		},
		"single doc with header": {
			input: token.Tokens{header(), str("key"), colon(), str("value")},
			want:  []int{4},
		},
		"doc with end marker": {
			input: token.Tokens{str("key"), colon(), str("value"), end()},
			want:  []int{4},
		},
		"end marker closes a document": {
			input: token.Tokens{str("a"), colon(), str("1"), end(), str("b"), colon(), str("2")},
			want:  []int{4, 3},
		},
		"end marker followed by header": {
			input: token.Tokens{str("a"), end(), header(), str("b")},
			want:  []int{2, 2},
		},
		"two docs": {
			input: token.Tokens{str("key1"), colon(), str("v1"), header(), str("key2"), colon(), str("v2")},
			want:  []int{3, 4},
		},
		"three docs with headers": {
			input: token.Tokens{header(), str("doc1"), header(), str("doc2"), header(), str("doc3")},
			want:  []int{2, 2, 2},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := collectDocs(tokens.SplitDocuments(tc.input))

			rest := requireDocs(t, tc.input, tc.want, got)
			assert.Empty(t, rest, "every token belongs to a document")
		})
	}

	t.Run("stops when the caller does", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input token.Tokens
			want  []int // Number of tokens in each document the caller reads.
		}{
			"at the first doc": {
				input: token.Tokens{str("doc1"), header(), str("doc2")},
				want:  []int{1},
			},
			"at the second doc": {
				input: token.Tokens{str("doc1"), header(), str("doc2"), header(), str("doc3")},
				want:  []int{1, 2},
			},
			"single doc": {
				input: token.Tokens{str("only")},
				want:  []int{1},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var got []token.Tokens

				for _, tks := range tokens.SplitDocuments(tc.input) {
					got = append(got, tks)

					if len(got) == len(tc.want) {
						break
					}
				}

				requireDocs(t, tc.input, tc.want, got)
			})
		}
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

	t.Run("yields the tokens passed in as they are", func(t *testing.T) {
		t.Parallel()

		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").
				PositionLine(5).PositionColumn(3).PositionOffset(100).Build(),
		}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 1)
		require.Len(t, got[0], 1)

		// The document holds the same pointer, not a clone, and the
		// token keeps its position.
		assert.Same(t, input[0], got[0][0])
		assert.Equal(t, []string{"5:3:100"}, positionsOf(got[0]))
	})
}

func TestResetPositions(t *testing.T) {
	t.Parallel()

	t.Run("moves each document to line 1", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		at := func(typ token.Type, value string, line, col, offset int) *token.Token {
			return tkb.Clone().Type(typ).Value(value).
				PositionLine(line).PositionColumn(col).PositionOffset(offset).Build()
		}

		tcs := map[string]struct {
			input token.Tokens
			want  [][]string // Position of each token of each document.
		}{
			// The first token moves to 1:1:1. Each token after it on the
			// first line keeps its distance from the first in columns and
			// in offset.
			"single document": {
				input: token.Tokens{
					at(token.StringType, "key", 5, 3, 100),
					at(token.MappingValueType, ":", 5, 6, 103),
					at(token.StringType, "value", 5, 8, 105),
				},
				want: [][]string{{"1:1:1", "1:4:4", "1:6:6"}},
			},
			// A token on a later line keeps its column, and its offset
			// counts from the first token.
			"multiline tokens within a document": {
				input: token.Tokens{
					at(token.LiteralType, "|", 10, 5, 50),
					at(token.StringType, "line1\nline2", 11, 5, 52),
				},
				want: [][]string{{"1:1:1", "2:5:3"}},
			},
			"each of two documents starts at line 1": {
				input: token.Tokens{
					at(token.StringType, "key1", 1, 1, 0),
					at(token.DocumentHeaderType, "---", 3, 1, 15),
					at(token.StringType, "key2", 4, 1, 20),
				},
				want: [][]string{{"1:1:1"}, {"1:1:1", "2:1:6"}},
			},
			"three documents reset independently": {
				input: token.Tokens{
					at(token.StringType, "doc1", 1, 1, 0),
					at(token.DocumentHeaderType, "---", 5, 1, 20),
					at(token.StringType, "doc2", 6, 1, 25),
					at(token.DocumentHeaderType, "---", 10, 1, 40),
					at(token.StringType, "doc3", 11, 1, 45),
				},
				want: [][]string{{"1:1:1"}, {"1:1:1", "2:1:6"}, {"1:1:1", "2:1:6"}},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				before := positionsOf(tc.input)
				docs := collectReset(tokens.SplitDocuments(tc.input))

				got := make([][]string, 0, len(docs))
				for _, doc := range docs {
					got = append(got, positionsOf(doc))
				}

				assert.Equal(t, tc.want, got)

				// The reset tokens are clones, so the tokens passed in
				// keep their positions.
				clones := slices.Concat(docs...)
				require.Len(t, clones, len(tc.input))

				for i, tk := range tc.input {
					assert.NotSame(t, tk, clones[i], "token %d", i)
				}

				assert.Equal(t, before, positionsOf(tc.input))
			})
		}
	})

	t.Run("keeps a blank line with a tab before the first key", func(t *testing.T) {
		t.Parallel()

		// The first token opens with the whole blank line, so a stream
		// that starts at line 1 keeps its positions.
		got := tokens.ResetPositions(tokens.Tokenize(" \t\na: 1\n"))

		require.NotEmpty(t, got)
		assert.Equal(t, " \t\na", got[0].Origin)
		assert.Equal(t, "2:1:4", positionsOf(got)[0])
	})

	t.Run("counts from the joined Origins after a swallowed indicator", func(t *testing.T) {
		t.Parallel()

		// The lexer swallows a "-" or ":" after a tab that indents the
		// first line. Tokenize counts the swallowed rune in its positions,
		// but the joined Origins lack it, so the clones of a whole stream
		// move to where the Origins hold their text.
		tcs := map[string]struct {
			input   string
			origins string
			want    []string
		}{
			"sequence entry before a key": {
				input:   "\t- a\nb: 1\n",
				origins: "\t a\nb: 1\n",
				want:    []string{"1:1:1", "1:3:3", "2:2:6", "2:4:8"},
			},
			"mapping value alone": {
				input:   "\t:\n",
				origins: "\t\n",
				want:    []string{"1:1:1"},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got := tokens.ResetPositions(tokens.Tokenize(tc.input))

				assert.Equal(t, tc.origins, yamltest.DumpTokenOrigins(got))
				assert.Equal(t, tc.want, positionsOf(got))
			})
		}
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

	t.Run("matches a fresh tokenize of the same text", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
		}{
			"after document header": {
				input: stringtest.LinesLF(
					"a: 1",
					"---",
					"b: 2",
				),
			},
			"after document end": {
				input: stringtest.LinesLF(
					"a: 1",
					"...",
					"b: 2",
				),
			},
			"comment after document end": {
				input: stringtest.LinesLF(
					"a: 1",
					"...",
					"# c",
					"b: 2",
				),
			},
			"comment on document end line": {
				input: stringtest.LinesLF(
					"a: 1",
					"... # c",
					"b: 2",
				),
			},
			"indented after document end": {
				input: stringtest.LinesLF(
					"a: 1",
					"...",
					"  b: 2",
				),
			},
			"header after document end": {
				input: stringtest.LinesLF(
					"a: 1",
					"...",
					"---",
					"b: 2",
				),
			},
			"header after directive": {
				input: stringtest.LinesLF(
					"%YAML 1.2",
					"---",
					"a: 1",
				),
			},
			"leading blank lines": {
				input: stringtest.LinesLF(
					"",
					"",
					"a: 1",
				),
			},
			"crlf after document end": {
				input: stringtest.LinesCRLF(
					"a: 1",
					"...",
					"b: 2",
				),
			},
			"bare cr after document end": {input: "a: 1\r...\rb: 2\r"},
			"bare cr blank line":         {input: "a: 1\r...\r\rb: 2\r"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				for i, doc := range tokens.SplitDocuments(lexer.Tokenize(tc.input)) {
					doc = tokens.ResetPositions(doc)
					// A document's text is the Origins of its tokens.
					fresh := lexer.Tokenize(yamltest.DumpTokenOrigins(doc))

					assert.Equal(t, positionsOf(fresh), positionsOf(doc), "document %d", i)
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
			return slices.DeleteFunc(slices.Clone(tks), blankOrigin)
		}

		tcs := map[string]struct {
			input string
		}{
			"tab before a key": {
				input: stringtest.LinesLF(
					"a: 1",
					"...",
					"\tb: 2",
				),
			},
			"tab before a comment": {
				input: stringtest.LinesLF(
					"a: 1",
					"...",
					"\t# c",
					"b: 2",
				),
			},
			"tab on a blank line": {
				input: stringtest.LinesLF(
					"a: 1",
					"...",
					"\t",
					"b: 2",
				),
			},
			"tab after a crlf line end": {
				input: stringtest.LinesLF(
					"a: 1",
					"...\r",
					"\tb: 2\r",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				for i, doc := range tokens.SplitDocuments(tokens.Tokenize(tc.input)) {
					doc = tokens.ResetPositions(doc)
					fresh := withText(tokens.Tokenize(yamltest.DumpTokenOrigins(doc)))

					assert.Equal(t, positionsOf(fresh), positionsOf(withText(doc)), "document %d", i)
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
			"empty literal": {
				input: stringtest.LinesLF(
					"a: |",
					"---",
					"b: 1",
				),
				want: "1:5:5",
			},
			"empty stripped literal": {
				input: stringtest.LinesLF(
					"a: |-",
					"---",
					"b: 1",
				),
				want: "1:6:6",
			},
			"nested empty literal": {
				input: stringtest.LinesLF(
					"x:",
					"  a: |",
					"---",
					"b: 1",
				),
				want: "2:7:10",
			},
			"blank line": {
				input: stringtest.LinesLF(
					"k: |",
					"",
					"---",
					"b: 1",
				),
				want: "2:1:6",
			},
			"blank line with a tab": {
				input: stringtest.LinesLF(
					"a: |",
					"\t",
					"---",
					"b: 1",
				),
				want: "2:2:7",
			},
			"empty literal in a sequence entry": {
				input: stringtest.LinesLF(
					"- |",
					"---",
					"- b",
				),
				want: "1:4:4",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				docs := collectReset(tokens.SplitDocuments(tokens.Tokenize(tc.input)))
				require.Len(t, docs, 2)

				doc := docs[0]
				last := doc[len(doc)-1]
				require.True(t, blankOrigin(last), "last origin %q", last.Origin)

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
			"empty literal": {
				input: stringtest.LinesLF(
					"a: |",
					"",
					"b: 1",
				),
			},
			"empty stripped literal": {
				input: stringtest.LinesLF(
					"a: |-",
					"",
					"b: 1",
				),
			},
			"empty folded": {
				input: stringtest.LinesLF(
					"a: >",
					"",
					"",
					"b: 1",
				),
			},
			"indented key after it": {
				input: stringtest.LinesLF(
					"a:",
					"  k: |",
					"",
					"  b: 1",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				tks := tokens.Tokenize(tc.input)
				cut := slices.IndexFunc(tks, func(tk *token.Token) bool { return tk.Origin == "" })
				require.GreaterOrEqual(t, cut, 0)

				reset := tokens.ResetPositions(tks[cut:])
				fresh := tokens.Tokenize(yamltest.DumpTokenOrigins(reset))
				require.Len(t, reset, len(fresh)+1)

				// ResetPositions leaves IndentNum and IndentLevel as they
				// are, so compare the line, column, and offset alone.
				got := positionsOf(reset)

				// The empty token shares the position of the token after it.
				assert.Equal(t, got[1], got[0])
				assert.Equal(t, positionsOf(fresh), got[1:])
			})
		}
	})

	t.Run("severs links to neighboring documents", func(t *testing.T) {
		t.Parallel()

		orig := lexer.Tokenize(stringtest.LinesLF(
			"a: 1",
			"...",
			"b: 2",
		))
		docs := collectReset(tokens.SplitDocuments(orig))
		require.Len(t, docs, 2)

		// The clones link to each other inside a document, and the chain
		// of each document stops at both of its ends.
		require.NoError(t, yamltest.ValidateTokenLinks(docs[0]))
		require.NoError(t, yamltest.ValidateTokenLinks(docs[1]))

		// The originals keep their links across the boundary.
		assert.Same(t, orig[4], orig[3].Next)
		assert.Same(t, orig[3], orig[4].Prev)
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
				input: stringtest.LinesLF(
					"a: 1",
					"...",
					"\t",
					"---",
					"b: 2",
				),
				doc:  1,
				want: 1,
			},
			"tab line after document end at the end": {
				input: stringtest.LinesLF(
					"a: 1",
					"...",
					"\t",
				),
				doc:  1,
				want: 1,
			},
			"two tab lines between document end and header": {
				input: stringtest.LinesLF(
					"a: 1",
					"...",
					"\t",
					"\t",
					"---",
					"b: 2",
				),
				doc:  1,
				want: 2,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var got token.Tokens

				for i, doc := range tokens.SplitDocuments(tokens.Tokenize(tc.input)) {
					if i == tc.doc {
						got = tokens.ResetPositions(doc)
					}
				}

				require.Len(t, got, tc.want)

				for i, pos := range positionsOf(got) {
					assert.Equal(t, "1:1:1", pos, "token %d", i)
				}
			})
		}
	})

	t.Run("returns an input with no tokens as it is", func(t *testing.T) {
		t.Parallel()

		assert.Empty(t, tokens.ResetPositions(token.Tokens{}))
		assert.Nil(t, tokens.ResetPositions(nil))
	})

	t.Run("skips tokens with nil position when finding start", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			&token.Token{Type: token.StringType, Value: "nil-pos", Position: nil},
			tkb.Clone().Type(token.StringType).Value("key").
				PositionLine(5).PositionColumn(3).PositionOffset(100).Build(),
		}

		got := tokens.ResetPositions(input)

		require.Len(t, got, 2)

		// First token still has nil position.
		assert.Nil(t, got[0].Position)

		// ResetPositions moves the second token to line 1.
		assert.Equal(t, []string{"1:1:1"}, positionsOf(got[1:]))
	})
}

func TestResetPositionsInPlace(t *testing.T) {
	t.Parallel()

	// Each document of a text resets in place to the tokens and the links
	// that ResetPositions gives its clones. The Origins of the tokens hold
	// the text of the document.
	tcs := map[string]struct {
		input string
	}{
		"whole stream": {
			input: "a: 1\nb:\n  - c\n",
		},
		"swallowed indicator": {
			input: "\t- a\nb: 1\n",
		},
		"documents cut from a stream": {
			input: "a: 1\n---\nb: 2\n...\nc: |\n---\nd: 3\n",
		},
		"block scalar before a header": {
			input: "a: |+\n---\nb: 1\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want := collectReset(tokens.SplitDocuments(tokens.Tokenize(tc.input)))

			var got []token.Tokens

			for _, doc := range tokens.SplitDocuments(tokens.Tokenize(tc.input)) {
				reset := tokens.ResetPositionsInPlace(doc)

				// The result holds the tokens of the document.
				require.Len(t, reset, len(doc))

				for i, tk := range reset {
					require.Same(t, doc[i], tk)
				}

				got = append(got, reset)
			}

			require.Len(t, got, len(want))

			for i := range want {
				assert.Equal(t, positionsOf(want[i]), positionsOf(got[i]))
				assert.Equal(t, origins(want[i]), origins(got[i]))
				assertLinked(t, got[i])
			}
		})
	}

	t.Run("drops nil tokens", func(t *testing.T) {
		t.Parallel()

		tks := tokens.Tokenize("a: 1\n")
		input := slices.Insert(slices.Clone(tks), 1, nil)

		got := tokens.ResetPositionsInPlace(input)

		require.Len(t, got, len(tks))
		assert.Equal(t, positionsOf(tokens.ResetPositions(tokens.Tokenize("a: 1\n"))), positionsOf(got))
		assertLinked(t, got)
	})
}

// origins returns the Origin of each token of tks.
func origins(tks token.Tokens) []string {
	got := make([]string, 0, len(tks))
	for _, tk := range tks {
		got = append(got, tk.Origin)
	}

	return got
}

// assertLinked asserts that the tokens of tks link to each other in order
// through Next and Prev, and to no token outside tks.
func assertLinked(t *testing.T, tks token.Tokens) {
	t.Helper()

	for i, tk := range tks {
		var prev, next *token.Token

		if i > 0 {
			prev = tks[i-1]
		}

		if i < len(tks)-1 {
			next = tks[i+1]
		}

		assert.Same(t, prev, tk.Prev, "token %d", i)
		assert.Same(t, next, tk.Next, "token %d", i)
	}
}

// blankOrigin reports whether the Origin of tk holds whitespace alone.
func blankOrigin(tk *token.Token) bool {
	return strings.Trim(tk.Origin, " \t\r\n") == ""
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
	for name, tc := range positionCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, positionsOf(tokens.Tokenize(tc.input)))
		})
	}
}

// positionSources returns by name the sources of [positionCorpus] and of
// the tables that hold sources of the same kind. A source that two tables
// hold goes by one name in both, so it runs once. The test fails when two
// different sources share a name.
func positionSources(t *testing.T) map[string]string {
	t.Helper()

	sources := maps.Clone(positionCorpus)
	add := func(name, input string) {
		if have, ok := sources[name]; ok {
			require.Equal(t, have, input, "two sources named %q", name)
		}

		sources[name] = input
	}

	for name, tc := range positionCases {
		add(name, tc.input)
	}

	for name, tc := range droppedWhitespace {
		add(name, tc.input)
	}

	for name, tc := range droppedLineBreaks {
		add(name, tc.input)
	}

	for name, input := range numericEscapes {
		add(name, input)
	}

	return sources
}

func TestTokenize_PositionsLocateText(t *testing.T) {
	t.Parallel()

	for name, input := range positionSources(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			runes := []rune(input)

			for i, tk := range tokens.Tokenize(input) {
				require.NotNil(t, tk.Position, "token %d", i)

				// The first line of text in the Origin is in the source at
				// Offset. Tokenize restores the Origin of a double-quoted
				// scalar from the source, so all of its text is there.
				text := firstTextLine(tk.Origin)
				if tk.Type == token.DoubleQuoteType {
					text = strings.Trim(tk.Origin, " \t\r\n")
				}

				assertLocates(t, runes, i, tk, text)
			}
		})
	}
}

// assertLocates asserts that the position of tk, the token at index i of
// the stream for the source runes, names one rune of the source. Line and
// Column follow from Offset: one more line per line break before the rune,
// and the runes since the last break. When text is not empty, the source
// holds it at Offset.
func assertLocates(t *testing.T, runes []rune, i int, tk *token.Token, text string) {
	t.Helper()

	at := tk.Position.Offset - 1
	require.GreaterOrEqual(t, at, 0, "token %d %q", i, tk.Origin)
	require.LessOrEqual(t, at, len(runes), "token %d %q", i, tk.Origin)

	before := string(runes[:at])
	lastBreak := strings.LastIndexAny(before, "\r\n")

	assert.Equal(t, 1+countLineBreaks(before), tk.Position.Line, "token %d %q line", i, tk.Origin)

	wantCol := utf8.RuneCountInString(before[lastBreak+1:]) + 1
	assert.Equal(t, wantCol, tk.Position.Column, "token %d %q column", i, tk.Origin)

	if text == "" {
		return
	}

	rest := string(runes[at:])
	assert.True(t, strings.HasPrefix(rest, text), "token %d %q at %d: %q", i, tk.Origin, at, rest)
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
	for name, input := range positionSources(t) {
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
