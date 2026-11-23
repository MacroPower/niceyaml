package yamlscan_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/lineend"
	"go.jacobcolvin.com/niceyaml/internal/yamlscan"
)

// Reading names a patch of the copy that changes the token types or
// values that go-yaml's lexer gives an input.
type reading string

const (
	// The copy closes an unclosed flow collection at the document marker,
	// but go-yaml's lexer reads the later documents as the inside of the
	// collection.
	readFlow reading = "flow collection closed at a document marker"
	// The copy emits a tag that ends the input, but go-yaml's lexer drops
	// it.
	readTag reading = "tag that ends the input"
	// The copy keeps the rune after a double-quoted scalar that holds a
	// tab between its text, but go-yaml's lexer skips that rune.
	readTab reading = "rune after a double-quoted scalar with a tab"
)

// Readings holds each input of the corpus whose token types or values
// differ from those of go-yaml's lexer, with the patch that makes them
// differ.
var readings = map[string]reading{
	"testdata/corpus/ny/020e1d155680ba18.yaml":                          readTab,
	"testdata/corpus/ny/0a63f443bad091c2.yaml":                          readFlow,
	"testdata/corpus/ny/170ab8343bfa65b2.yaml":                          readFlow,
	"testdata/corpus/ny/1c1e6fbd6da10194.yaml":                          readFlow,
	"testdata/corpus/ny/1c811ebbe7b2f54a.yaml":                          readFlow,
	"testdata/corpus/ny/372d2e9802b34509.yaml":                          readTag,
	"testdata/corpus/ny/37767ad8a8c8bb88.yaml":                          readFlow,
	"testdata/corpus/ny/49633592044d7a83.yaml":                          readTag,
	"testdata/corpus/ny/58ff94c6d6941ae1.yaml":                          readTag,
	"testdata/corpus/ny/6fe609943a0f0183.yaml":                          readFlow,
	"testdata/corpus/ny/7639c9a81a881762.yaml":                          readFlow,
	"testdata/corpus/ny/8389eaccdfc90948.yaml":                          readTag,
	"testdata/corpus/ny/873ef8d7ce64ae87.yaml":                          readFlow,
	"testdata/corpus/ny/8c8891b0297506d0.yaml":                          readFlow,
	"testdata/corpus/ny/8cf93a89ea05cbf0.yaml":                          readFlow,
	"testdata/corpus/ny/8d700bfa14c0a453.yaml":                          readTag,
	"testdata/corpus/ny/9e82b5ac83955dec.yaml":                          readFlow,
	"testdata/corpus/ny/bce30c8a70314e21.yaml":                          readFlow,
	"testdata/corpus/ny/c48f78e6fb5fa6fd.yaml":                          readTag,
	"testdata/corpus/ny/c5cd9ce6e5f815f1.yaml":                          readFlow,
	"testdata/corpus/ny/c72f638b107edba4.yaml":                          readFlow,
	"testdata/corpus/ny/cd9937f542fa35fe.yaml":                          readFlow,
	"testdata/corpus/ny/d1891338a74debe0.yaml":                          readTag,
	"testdata/corpus/ny/d3b1448f35c9e6d8.yaml":                          readFlow,
	"testdata/corpus/ny/da44b184c7cf3b21.yaml":                          readTag,
	"testdata/corpus/ny/f309731e7ef738c9.yaml":                          readTag,
	"testdata/corpus/suite/invalid-document-markers-in-flow-style.yaml": readFlow,
}

// place is the line, the column, and the rune offset of a rune, each
// counted from 1.
type place struct {
	Line, Column, Offset int
}

// joined returns the Origins of tks joined.
func joined(tks token.Tokens) string {
	var sb strings.Builder

	for _, tk := range tks {
		sb.WriteString(tk.Origin)
	}

	return sb.String()
}

// wantPlaces returns the place where each token of tks belongs in the
// joined Origins. A token that holds text belongs at the first rune of
// its text. A token without text belongs where the next text starts, or
// at the end of the last line when no text follows it. When the Origins
// hold whitespace alone, every token belongs at the first rune.
func wantPlaces(tks token.Tokens) []place {
	text := joined(tks)
	if strings.Trim(text, " \t\r\n") == "" {
		out := make([]place, len(tks))
		for i := range out {
			out[i] = place{Line: 1, Column: 1, Offset: 1}
		}

		return out
	}

	// The place of each rune of text, and of the rune past its end.
	places := make([]place, 0, utf8.RuneCountInString(text)+1)
	line, col := 1, 1

	for ln := range lineend.Lines(text) {
		for range ln {
			places = append(places, place{Line: line, Column: col, Offset: len(places) + 1})
			col++
		}

		if lineend.Trim(ln) != ln {
			line, col = line+1, 1
		}
	}

	places = append(places, place{Line: line, Column: col, Offset: len(places) + 1})

	endLine, endCol := lineend.Advance(1, 1, lineend.Trim(text))
	end := place{Line: endLine, Column: endCol, Offset: utf8.RuneCountInString(lineend.Trim(text)) + 1}

	out := make([]place, len(tks))
	pending := []int{}
	off := 0

	for i, tk := range tks {
		rest := strings.TrimLeft(tk.Origin, " \t\r\n")
		if rest == "" {
			pending = append(pending, i)
			off += utf8.RuneCountInString(tk.Origin)

			continue
		}

		at := places[off+utf8.RuneCountInString(tk.Origin)-utf8.RuneCountInString(rest)]
		for _, j := range append(pending, i) {
			out[j] = at
		}

		pending = pending[:0]
		off += utf8.RuneCountInString(tk.Origin)
	}

	for _, j := range pending {
		out[j] = end
	}

	return out
}

// typedValue holds the type and the value of a token.
type typedValue struct {
	Value string
	Type  token.Type
}

// typedValues returns the type and the value of each token of tks, and
// reports whether tks holds an invalid token.
func typedValues(tks token.Tokens) ([]typedValue, bool) {
	out := make([]typedValue, len(tks))
	invalid := false

	for i, tk := range tks {
		out[i] = typedValue{Type: tk.Type, Value: tk.Value}
		invalid = invalid || tk.Type == token.InvalidType
	}

	return out, invalid
}

// gotPlaces returns the place that the position of each token of tks
// names.
func gotPlaces(tks token.Tokens) []place {
	out := make([]place, len(tks))
	for i, tk := range tks {
		out[i] = place{Line: tk.Position.Line, Column: tk.Position.Column, Offset: tk.Position.Offset}
	}

	return out
}

// TestTokenize reads every input of the corpus. The Origins join to the
// source, no two tokens cut a CRLF, and each position names the place
// where its token belongs. The tokens have the types and values that
// go-yaml's lexer gives them, except for the inputs of readings. The test
// skips that check where either stream holds an invalid token, and where
// the lexer gives no token, as for a source of whitespace alone.
func TestTokenize(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob(filepath.Join("testdata", "corpus", "*", "*.yaml"))
	require.NoError(t, err)
	require.Len(t, files, 1582)

	for _, file := range files {
		name := filepath.ToSlash(file)

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			src, err := os.ReadFile(file)
			require.NoError(t, err)

			tks := yamlscan.Tokenize(string(src))

			want, wantInvalid := typedValues(lexer.Tokenize(string(src)))
			got, gotInvalid := typedValues(tks)

			switch {
			case len(want) == 0 || wantInvalid || gotInvalid:
			case readings[name] != "":
				assert.NotEqual(t, want, got, "types and values of a %s", readings[name])
			default:
				assert.Equal(t, want, got, "types and values")
			}

			assert.Equal(t, string(src), joined(tks))

			// A CRLF stays whole in one Origin.
			for i := 1; i < len(tks); i++ {
				if strings.HasSuffix(tks[i-1].Origin, "\r") && strings.HasPrefix(tks[i].Origin, "\n") {
					assert.Fail(t, "CRLF cut", "token %d %q", i, tks[i].Origin)
				}
			}

			// A diff of the whole stream of a large input takes minutes,
			// so the test reports the first token that differs.
			wantAt, gotAt := wantPlaces(tks), gotPlaces(tks)
			for i := range wantAt {
				if !assert.Equal(t, wantAt[i], gotAt[i], "token %d %q", i, tks[i].Origin) {
					break
				}
			}
		})
	}
}

// tok holds the type and the Origin of a token.
type tok struct {
	Type   token.Type
	Origin string
}

// toks returns the type and the Origin of each token of tks.
func toks(tks token.Tokens) []tok {
	out := make([]tok, len(tks))
	for i, tk := range tks {
		out[i] = tok{Type: tk.Type, Origin: tk.Origin}
	}

	return out
}

// TestTokenize_Patches reads one input for each patch of the copy.
func TestTokenize_Patches(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  []tok
	}{
		"trailing spaces before a key": {
			input: "a: b  \n? c\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.StringType, " b  \n"},
				{token.MappingKeyType, "?"},
				{token.StringType, " c\n"},
			},
		},
		"whitespace alone": {
			input: "  \n",
			want:  []tok{{token.StringType, "  \n"}},
		},
		"space before the colon of an alias key": {
			input: "*a : 2\n",
			want: []tok{
				{token.AliasType, "*"},
				{token.StringType, "a"},
				{token.MappingValueType, " :"},
				{token.IntegerType, " 2\n"},
			},
		},
		"hex escape": {
			input: `a: "\x41"` + "\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.DoubleQuoteType, ` "\x41"` + "\n"},
			},
		},
		"unicode escape that swallows the quote": {
			input: `a: "\u12"` + "\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.InvalidType, ` "\u12"` + "\n"},
			},
		},
		"abandoned unicode escape": {
			// The scan reads the escape again from its backslash.
			input: `a: "\u: 1`,
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.InvalidType, ` "`},
				{token.StringType, `\u`},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1"},
			},
		},
		"crlf after a comment": {
			input: "# c\r\nd: 1\r\n",
			want: []tok{
				{token.CommentType, "# c\r\n"},
				{token.StringType, "d"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\r\n"},
			},
		},
		"crlf after an alias": {
			input: "- *a\r\n- b\r\n",
			want: []tok{
				{token.SequenceEntryType, "-"},
				{token.AliasType, " *"},
				{token.StringType, "a"},
				{token.SequenceEntryType, "\r\n-"},
				{token.StringType, " b\r\n"},
			},
		},
		"crlf after a tag": {
			input: "a: !t\r\nb: 1\r\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.TagType, " !t\r\n"},
				{token.StringType, "b"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\r\n"},
			},
		},
		"text after a block scalar header": {
			input: "a: |x\nb: 1\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.InvalidType, " |x\n"},
				{token.StringType, "b"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\n"},
			},
		},
		"text after a block scalar header that ends the input": {
			input: `key: >"q"`,
			want: []tok{
				{token.StringType, "key"},
				{token.MappingValueType, ":"},
				{token.InvalidType, ` >"q"`},
			},
		},
		"tab before a sequence entry": {
			input: "\t- a\n",
			want: []tok{
				{token.InvalidType, "\t-"},
				{token.StringType, " a\n"},
			},
		},
		"tab that indents a later line of a quoted scalar": {
			input: "a: \"x\n\ty\"\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.InvalidType, " \"x\n\t"},
				{token.StringType, "y\"\n"},
			},
		},
		"tab that indents a line after a tab in a double-quoted scalar": {
			input: "a: \"x\ty\n\t\n---\nb: 1\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.InvalidType, " \"x\ty\n\t"},
				{token.DocumentHeaderType, "\n---"},
				{token.StringType, "\nb"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\n"},
			},
		},
		"comment on a line that a tab indents after a tab in a double-quoted scalar": {
			input: "a: \"\t]\n\t#c\nb: 1\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.InvalidType, " \"\t]\n\t"},
				{token.CommentType, "#c\n"},
				{token.StringType, "b"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\n"},
			},
		},
		"tab before a sequence entry on the line after a tag": {
			input: "!t\n\t- a\n",
			want: []tok{
				{token.TagType, "!t\n"},
				{token.SequenceEntryType, "\t-"},
				{token.StringType, " a\n"},
			},
		},
		"tab before a key on the line after a comment that ends with a crlf": {
			input: "# c\r\n\ta: 1\r\n",
			want: []tok{
				{token.CommentType, "# c\r\n"},
				{token.StringType, "\ta"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\r\n"},
			},
		},
		"tab before a key on the line after an invalid block scalar header": {
			// Go-yaml leaves the line ending of the header to the next
			// scan, so it reads the key, and the "---" ends the document.
			input: "|x\n\ta: |\n---\nc: 1\n",
			want: []tok{
				{token.InvalidType, "|x\n"},
				{token.StringType, "\ta"},
				{token.MappingValueType, ":"},
				{token.LiteralType, " |\n"},
				{token.StringType, ""},
				{token.DocumentHeaderType, "---"},
				{token.StringType, "\nc"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\n"},
			},
		},
		"flow sequence open at a header": {
			input: "a: [b\n---\nk: v, w\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.SequenceStartType, " ["},
				{token.StringType, "b\n"},
				{token.DocumentHeaderType, "---"},
				{token.StringType, "\nk"},
				{token.MappingValueType, ":"},
				{token.StringType, " v, w\n"},
			},
		},
		"flow sequence open at a document end": {
			input: "a: [b\n...\nm: x, y\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.SequenceStartType, " ["},
				{token.StringType, "b\n"},
				{token.DocumentEndType, "..."},
				{token.StringType, "\nm"},
				{token.MappingValueType, ":"},
				{token.StringType, " x, y\n"},
			},
		},
		"flow sequence after a sequence end that closes nothing": {
			input: "a: ]\n---\nx: [a, b]\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.SequenceEndType, " ]"},
				{token.DocumentHeaderType, "\n---"},
				{token.StringType, "\nx"},
				{token.MappingValueType, ":"},
				{token.SequenceStartType, " ["},
				{token.StringType, "a"},
				{token.CollectEntryType, ","},
				{token.StringType, " b"},
				{token.SequenceEndType, "]\n"},
			},
		},
		"tag that ends the input": {
			input: "a: !t",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.TagType, " !t"},
			},
		},
		"tag before the key of the next line": {
			input: "a: !t\n1: x\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.TagType, " !t\n"},
				{token.IntegerType, "1"},
				{token.MappingValueType, ":"},
				{token.StringType, " x\n"},
			},
		},
		"tag on a mapping": {
			input: "!t\n1: x\n",
			want: []tok{
				{token.TagType, "!t\n"},
				{token.IntegerType, "1"},
				{token.MappingValueType, ":"},
				{token.StringType, " x\n"},
			},
		},
		"anchor after a tab after a tag": {
			input: "a: !t\t&x 1\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.TagType, " !t\t"},
				{token.AnchorType, "&"},
				{token.StringType, "x"},
				{token.IntegerType, " 1\n"},
			},
		},
		"block scalar header after a tab after a tag": {
			input: "a: !!str\t|\n  x\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.TagType, " !!str\t"},
				{token.LiteralType, "|\n"},
				{token.StringType, "  x\n"},
			},
		},
		"scalar after a tab after a tag in a flow sequence": {
			input: "[!t\t1, 2]\n",
			want: []tok{
				{token.SequenceStartType, "["},
				{token.TagType, "!t\t"},
				{token.StringType, "1"},
				{token.CollectEntryType, ","},
				{token.IntegerType, " 2"},
				{token.SequenceEndType, "]\n"},
			},
		},
		"comment after a tab after a tag": {
			input: "a: !t\t#c\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.TagType, " !t\t"},
				{token.CommentType, "#c\n"},
			},
		},
		"value after a tab after an anchor": {
			input: "a: &x\t1\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.AnchorType, " &"},
				{token.StringType, "x\t"},
				{token.IntegerType, "1\n"},
			},
		},
		"value after a tab and a space after an anchor": {
			input: "a: &x\t 1\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.AnchorType, " &"},
				{token.StringType, "x\t"},
				{token.IntegerType, " 1\n"},
			},
		},
		"value after a tab after an anchor in a flow sequence": {
			input: "[&x\t1, *x]\n",
			want: []tok{
				{token.SequenceStartType, "["},
				{token.AnchorType, "&"},
				{token.StringType, "x\t"},
				{token.IntegerType, "1"},
				{token.CollectEntryType, ","},
				{token.AliasType, " *"},
				{token.StringType, "x"},
				{token.SequenceEndType, "]\n"},
			},
		},
		"tag after a tab after an anchor": {
			input: "a: &x\t!t 1\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.AnchorType, " &"},
				{token.StringType, "x\t"},
				{token.TagType, "!t "},
				{token.StringType, "1\n"},
			},
		},
		"colon after a tab after an alias": {
			input: "*a\t: 2\n",
			want: []tok{
				{token.AliasType, "*"},
				{token.StringType, "a\t"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 2\n"},
			},
		},
		"key after a tab after an anchor": {
			input: "&x\ta: 1\n",
			want: []tok{
				{token.AnchorType, "&"},
				{token.StringType, "x\t"},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\n"},
			},
		},
		"key after tabs and a space after an anchor": {
			input: "&x\t \ta: 1\n",
			want: []tok{
				{token.AnchorType, "&"},
				{token.StringType, "x\t \t"},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\n"},
			},
		},
		"key after a tab and a space after an anchor": {
			input: "&x\t a: 1\n",
			want: []tok{
				{token.AnchorType, "&"},
				{token.StringType, "x\t"},
				{token.StringType, " a"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\n"},
			},
		},
		"colon after a tab and a space after an alias": {
			input: "*a\t : 2\n",
			want: []tok{
				{token.AliasType, "*"},
				{token.StringType, "a"},
				{token.MappingValueType, "\t :"},
				{token.IntegerType, " 2\n"},
			},
		},
		"key after a space and a tab after an anchor": {
			input: "&x \ta: 1\n",
			want: []tok{
				{token.AnchorType, "&"},
				{token.StringType, "x \t"},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\n"},
			},
		},
		"key after a space, a tab, and a space after an anchor": {
			input: "&x \t a: 1\n",
			want: []tok{
				{token.AnchorType, "&"},
				{token.StringType, "x \t"},
				{token.StringType, " a"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\n"},
			},
		},
		"value after a space and a tab after an anchor": {
			input: "a: &x \t1\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.AnchorType, " &"},
				{token.StringType, "x \t"},
				{token.IntegerType, "1\n"},
			},
		},
		"colon after a space and a tab after an alias": {
			input: "*a \t: 2\n",
			want: []tok{
				{token.AliasType, "*"},
				{token.StringType, "a \t"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 2\n"},
			},
		},
		"line break after a space and a tab after an anchor": {
			input: "a: &x \t\nb: 1\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.AnchorType, " &"},
				{token.StringType, "x \t\n"},
				{token.StringType, "b"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\n"},
			},
		},
		"key after a space and a tab after a tag": {
			input: "!t \ta: 1\n",
			want: []tok{
				{token.TagType, "!t \t"},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\n"},
			},
		},
		"key after tabs and a space after a tag": {
			input: "!t\t \t a: 1\n",
			want: []tok{
				{token.TagType, "!t\t \t"},
				{token.StringType, " a"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\n"},
			},
		},
		"value after a space and a tab after a tag": {
			input: "a: !t \t1\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.TagType, " !t \t"},
				{token.StringType, "1\n"},
			},
		},
		"line break after a space and a tab after a tag": {
			input: "a: !t \t\nb: 1\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.TagType, " !t \t\n"},
				{token.StringType, "b"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\n"},
			},
		},
		"key after a space and a tab after a tag in a flow mapping": {
			input: "{!t \ta: 1}\n",
			want: []tok{
				{token.MappingStartType, "{"},
				{token.TagType, "!t \t"},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1"},
				{token.MappingEndType, "}\n"},
			},
		},
		"key after a tab after an anchor in a flow mapping": {
			input: "{&x\ta: 1}\n",
			want: []tok{
				{token.MappingStartType, "{"},
				{token.AnchorType, "&"},
				{token.StringType, "x\t"},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1"},
				{token.MappingEndType, "}\n"},
			},
		},
		"line break after a tab after an anchor": {
			input: "a: &x\t\nb: 1\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.AnchorType, " &"},
				{token.StringType, "x\t\n"},
				{token.StringType, "b"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\n"},
			},
		},
		"line break after a tab after an alias": {
			input: "a: *x\t\nb: 1\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.AliasType, " *"},
				{token.StringType, "x\t\n"},
				{token.StringType, "b"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\n"},
			},
		},
		"crlf after a tab after an anchor": {
			input: "a: &x\t\r\nb: 1\r\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.AnchorType, " &"},
				{token.StringType, "x\t\r\n"},
				{token.StringType, "b"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\r\n"},
			},
		},
		"line break after tabs and a space after an anchor": {
			input: "a: &x\t \t\n  b: 1\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.AnchorType, " &"},
				{token.StringType, "x\t \t\n"},
				{token.StringType, "  b"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\n"},
			},
		},
		"tab after an error after an anchor": {
			// The scan after the error starts at the tab, outside the name.
			input: "a: &!{\t1\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.AnchorType, " &"},
				{token.InvalidType, "!{"},
				{token.IntegerType, "\t1\n"},
			},
		},
		"tab in a scalar after a tag after an anchor": {
			input: "a: &!t 1\t2\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.AnchorType, " &"},
				{token.TagType, "!t "},
				{token.StringType, "1\t2\n"},
			},
		},
		"space after a double-quoted scalar with a tab": {
			input: "a: \"t\tb\" \nb: 1\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.DoubleQuoteType, " \"t\tb\" \n"},
				{token.StringType, "b"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\n"},
			},
		},
		"invalid escape after a tab in a double-quoted scalar": {
			// Go-yaml steps one rune past the text after the tab, so its
			// invalid token holds the "q".
			input: "a: \"x\t\\q\"\nb: 1\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.InvalidType, " \"x\t\\"},
				{token.StringType, "q\"\n"},
				{token.StringType, "b"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\n"},
			},
		},
		"crlf after an invalid double-quoted scalar with two tabs": {
			// Go-yaml steps one rune past the text after each tab, so it
			// skips the "q" and the "\r" of the CRLF.
			input: "a: \"\tx\ty\\q\r\nb: 1\r\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.InvalidType, " \"\tx\ty\\"},
				{token.StringType, "q\r\n"},
				{token.StringType, "b"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\r\n"},
			},
		},
		"crlf after a double-quoted scalar with two tabs": {
			// Go-yaml steps one rune past the text after each tab, so it
			// skips the "y" and the "\r" of the CRLF.
			input: "a: \"\t\tx\"y\r\nb: 1\r\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.DoubleQuoteType, " \"\t\tx\""},
				{token.StringType, "y\r\n"},
				{token.StringType, "b"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\r\n"},
			},
		},
		"crlf after a double-quoted scalar with a tab": {
			input: "a: \"t\tb\"\r\nb: 1\r\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.DoubleQuoteType, " \"t\tb\""},
				{token.StringType, "\r\nb"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 1\r\n"},
			},
		},
		"dash that continues a value of a flow mapping": {
			input: "{a: b\n - c}\n",
			want: []tok{
				{token.MappingStartType, "{"},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.StringType, " b\n - c"},
				{token.MappingEndType, "}\n"},
			},
		},
		"dash that continues a value right of its key": {
			input: "{a: b\n    - c}\n",
			want: []tok{
				{token.MappingStartType, "{"},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.StringType, " b\n    - c"},
				{token.MappingEndType, "}\n"},
			},
		},
		"dash that continues an explicit key of a flow mapping": {
			input: "{? a\n - b}\n",
			want: []tok{
				{token.MappingStartType, "{"},
				{token.MappingKeyType, "?"},
				{token.StringType, " a\n - b"},
				{token.MappingEndType, "}\n"},
			},
		},
		"key on two lines of a flow mapping": {
			input: "{x: y, a\n b: c}\n",
			want: []tok{
				{token.MappingStartType, "{"},
				{token.StringType, "x"},
				{token.MappingValueType, ":"},
				{token.StringType, " y"},
				{token.CollectEntryType, ","},
				{token.StringType, " a\n b"},
				{token.MappingValueType, ":"},
				{token.StringType, " c"},
				{token.MappingEndType, "}\n"},
			},
		},
		// YAML ends the value at the ":" of the key below it. The copy
		// cuts the value at the line of the key, as go-yaml's lexer does,
		// so the parser refuses the key.
		"key on the line below a value of a flow mapping": {
			input: "{a: b\n c: d}\n",
			want: []tok{
				{token.MappingStartType, "{"},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.StringType, " b\n "},
				{token.StringType, "c"},
				{token.MappingValueType, ":"},
				{token.StringType, " d"},
				{token.MappingEndType, "}\n"},
			},
		},
		"key on the line below a tagged value of a flow mapping": {
			input: "{a: !t b\n c: d}\n",
			want: []tok{
				{token.MappingStartType, "{"},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.TagType, " !t "},
				{token.StringType, "b\n "},
				{token.StringType, "c"},
				{token.MappingValueType, ":"},
				{token.StringType, " d"},
				{token.MappingEndType, "}\n"},
			},
		},
		// YAML takes a key on two lines in a flow mapping but not in a
		// flow sequence.
		"key on two lines of a flow sequence in a flow mapping": {
			input: "{x: [a\n b: c]}\n",
			want: []tok{
				{token.MappingStartType, "{"},
				{token.StringType, "x"},
				{token.MappingValueType, ":"},
				{token.SequenceStartType, " ["},
				{token.StringType, "a\n "},
				{token.StringType, "b"},
				{token.MappingValueType, ":"},
				{token.StringType, " c"},
				{token.SequenceEndType, "]"},
				{token.MappingEndType, "}\n"},
			},
		},
		"tab that opens a line of a value of a flow mapping": {
			input: "{a: b\n\tc}\n",
			want: []tok{
				{token.MappingStartType, "{"},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.StringType, " b\n\tc"},
				{token.MappingEndType, "}\n"},
			},
		},
		"explicit key on two lines before its value": {
			input: "{? a\n b: c}\n",
			want: []tok{
				{token.MappingStartType, "{"},
				{token.MappingKeyType, "?"},
				{token.StringType, " a\n b"},
				{token.MappingValueType, ":"},
				{token.StringType, " c"},
				{token.MappingEndType, "}\n"},
			},
		},
		"key on the line below an anchored value of a flow mapping": {
			input: "{a: &x b\n c: d}\n",
			want: []tok{
				{token.MappingStartType, "{"},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.AnchorType, " &"},
				{token.StringType, "x"},
				{token.StringType, " b\n "},
				{token.StringType, "c"},
				{token.MappingValueType, ":"},
				{token.StringType, " d"},
				{token.MappingEndType, "}\n"},
			},
		},
		// The lexer types the anchor name 1 as an integer.
		"key on the line below a value with a number anchor name": {
			input: "{a: &1 b\n c: d}\n",
			want: []tok{
				{token.MappingStartType, "{"},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.AnchorType, " &"},
				{token.IntegerType, "1"},
				{token.StringType, " b\n "},
				{token.StringType, "c"},
				{token.MappingValueType, ":"},
				{token.StringType, " d"},
				{token.MappingEndType, "}\n"},
			},
		},
		"key on two lines of a flow mapping in a flow sequence": {
			input: "[{x: [y], a\n b: c}]\n",
			want: []tok{
				{token.SequenceStartType, "["},
				{token.MappingStartType, "{"},
				{token.StringType, "x"},
				{token.MappingValueType, ":"},
				{token.SequenceStartType, " ["},
				{token.StringType, "y"},
				{token.SequenceEndType, "]"},
				{token.CollectEntryType, ","},
				{token.StringType, " a\n b"},
				{token.MappingValueType, ":"},
				{token.StringType, " c"},
				{token.MappingEndType, "}"},
				{token.SequenceEndType, "]\n"},
			},
		},
		"key on two lines of a flow sequence after a flow mapping": {
			input: "{x: [{y: z}, a\n b: c]}\n",
			want: []tok{
				{token.MappingStartType, "{"},
				{token.StringType, "x"},
				{token.MappingValueType, ":"},
				{token.SequenceStartType, " ["},
				{token.MappingStartType, "{"},
				{token.StringType, "y"},
				{token.MappingValueType, ":"},
				{token.StringType, " z"},
				{token.MappingEndType, "}"},
				{token.CollectEntryType, ","},
				{token.StringType, " a\n "},
				{token.StringType, "b"},
				{token.MappingValueType, ":"},
				{token.StringType, " c"},
				{token.SequenceEndType, "]"},
				{token.MappingEndType, "}\n"},
			},
		},
		"key below an item of an open flow sequence": {
			input: "a: [1\nb: 2\n",
			want: []tok{
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.SequenceStartType, " ["},
				{token.IntegerType, "1\n"},
				{token.StringType, "b"},
				{token.MappingValueType, ":"},
				{token.IntegerType, " 2\n"},
			},
		},
		// YAML refuses a key on a line that continues a value, also right
		// of the key of the value. The copy cuts the value at the line, and
		// it reads a "-" that opens the line as a sequence entry.
		"key on a dash line right of the key of a value": {
			input: "{a: b\n    - c: d}\n",
			want: []tok{
				{token.MappingStartType, "{"},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.StringType, " b\n    "},
				{token.SequenceEntryType, "-"},
				{token.StringType, " c"},
				{token.MappingValueType, ":"},
				{token.StringType, " d"},
				{token.MappingEndType, "}\n"},
			},
		},
		"key on a dash line below an item of a nested flow sequence": {
			input: "{a: [b\n    - c: d]}\n",
			want: []tok{
				{token.MappingStartType, "{"},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.SequenceStartType, " ["},
				{token.StringType, "b\n    "},
				{token.SequenceEntryType, "-"},
				{token.StringType, " c"},
				{token.MappingValueType, ":"},
				{token.StringType, " d"},
				{token.SequenceEndType, "]"},
				{token.MappingEndType, "}\n"},
			},
		},
		"key on a dash line below a value of a single pair": {
			input: "[a: b\n - c: d]\n",
			want: []tok{
				{token.SequenceStartType, "["},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.StringType, " b\n "},
				{token.SequenceEntryType, "-"},
				{token.StringType, " c"},
				{token.MappingValueType, ":"},
				{token.StringType, " d"},
				{token.SequenceEndType, "]\n"},
			},
		},
		// A tab after the "-" or "?" that opens the cut line is the blank
		// of the indicator, and the next token keeps it in its origin. The
		// parser refuses that token as a key after a tab, as go-yaml's
		// lexer reads the line.
		"key on a dash line with a tab below a value of a single pair": {
			input: "[a: b\n -\tc: d]\n",
			want: []tok{
				{token.SequenceStartType, "["},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.StringType, " b\n "},
				{token.SequenceEntryType, "-"},
				{token.InvalidType, "\tc:"},
				{token.StringType, " d"},
				{token.SequenceEndType, "]\n"},
			},
		},
		"key on a question mark line below a value of a single pair": {
			input: "[a: b\n ? c: d]\n",
			want: []tok{
				{token.SequenceStartType, "["},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.StringType, " b\n "},
				{token.MappingKeyType, "?"},
				{token.StringType, " c"},
				{token.MappingValueType, ":"},
				{token.StringType, " d"},
				{token.SequenceEndType, "]\n"},
			},
		},
		"key on a question mark line with a tab below a value of a single pair": {
			input: "[a: b\n ?\tc: d]\n",
			want: []tok{
				{token.SequenceStartType, "["},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.StringType, " b\n "},
				{token.MappingKeyType, "?"},
				{token.InvalidType, "\tc:"},
				{token.StringType, " d"},
				{token.SequenceEndType, "]\n"},
			},
		},
		"key without a value below a value of a single pair": {
			input: "[a: b\n c:]\n",
			want: []tok{
				{token.SequenceStartType, "["},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.StringType, " b\n "},
				{token.StringType, "c"},
				{token.MappingValueType, ":"},
				{token.SequenceEndType, "]\n"},
			},
		},
		"key on two lines of a flow sequence right of its start": {
			input: "[a\n b: c]\n",
			want: []tok{
				{token.SequenceStartType, "["},
				{token.StringType, "a\n "},
				{token.StringType, "b"},
				{token.MappingValueType, ":"},
				{token.StringType, " c"},
				{token.SequenceEndType, "]\n"},
			},
		},
		// YAML folds a line of a flow collection only when the line is
		// indented past the block node that holds the collection.
		"dash line at the indent of the key of a flow sequence": {
			input: "key: [a\n- c]\n",
			want: []tok{
				{token.StringType, "key"},
				{token.MappingValueType, ":"},
				{token.SequenceStartType, " ["},
				{token.StringType, "a\n"},
				{token.SequenceEntryType, "-"},
				{token.StringType, " c"},
				{token.SequenceEndType, "]\n"},
			},
		},
		"dash line right of the key of a nested flow mapping": {
			input: "x:\n  key: {a: b\n   - c}\n",
			want: []tok{
				{token.StringType, "x"},
				{token.MappingValueType, ":"},
				{token.StringType, "\n  key"},
				{token.MappingValueType, ":"},
				{token.MappingStartType, " {"},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.StringType, " b\n   - c"},
				{token.MappingEndType, "}\n"},
			},
		},
		// The indent of the block node is the column of the anchor of the
		// key, so a line at that column ends the value.
		"dash line at the indent of an anchored key of a nested flow mapping": {
			input: "x:\n  &k key: {a: b\n  - c}\n",
			want: []tok{
				{token.StringType, "x"},
				{token.MappingValueType, ":"},
				{token.AnchorType, "\n  &"},
				{token.StringType, "k"},
				{token.StringType, " key"},
				{token.MappingValueType, ":"},
				{token.MappingStartType, " {"},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.StringType, " b\n  "},
				{token.SequenceEntryType, "-"},
				{token.StringType, " c"},
				{token.MappingEndType, "}\n"},
			},
		},
		// YAML keeps a ":" in a plain scalar when a character other than a
		// blank or a flow indicator follows it, so the value spans the
		// lines.
		"colon before text on the line below a tagged value of a flow mapping": {
			input: "{a: !t b\n c :d}\n",
			want: []tok{
				{token.MappingStartType, "{"},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.TagType, " !t "},
				{token.StringType, "b\n c :d"},
				{token.MappingEndType, "}\n"},
			},
		},
		"colon before text on the line below an anchored value of a flow mapping": {
			input: "{a: &x b\n c :d}\n",
			want: []tok{
				{token.MappingStartType, "{"},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.AnchorType, " &"},
				{token.StringType, "x"},
				{token.StringType, " b\n c :d"},
				{token.MappingEndType, "}\n"},
			},
		},
		// Such a ":" ends a key of a flow mapping, as go-yaml reads it in
		// "{a b:c}" on one line.
		"colon before text on the line below a key of a flow mapping": {
			input: "{a\n b:c}\n",
			want: []tok{
				{token.MappingStartType, "{"},
				{token.StringType, "a\n b"},
				{token.MappingValueType, ":"},
				{token.StringType, "c"},
				{token.MappingEndType, "}\n"},
			},
		},
		// A ":" before a flow indicator ends an explicit key on two lines
		// of a flow sequence.
		"colon before the end of a flow sequence after an explicit key on two lines": {
			input: "key: [? a\n c:]\n",
			want: []tok{
				{token.StringType, "key"},
				{token.MappingValueType, ":"},
				{token.SequenceStartType, " ["},
				{token.MappingKeyType, "?"},
				{token.StringType, " a\n c"},
				{token.MappingValueType, ":"},
				{token.SequenceEndType, "]\n"},
			},
		},
		// The value above the key becomes two tokens, so the go-yaml
		// parser refuses the key after the single pair.
		"key below a value on two lines of a single pair in a flow sequence": {
			input: "[a: b\n c\n d: e]\n",
			want: []tok{
				{token.SequenceStartType, "["},
				{token.StringType, "a"},
				{token.MappingValueType, ":"},
				{token.StringType, " b\n "},
				{token.StringType, "c\n "},
				{token.StringType, "d"},
				{token.MappingValueType, ":"},
				{token.StringType, " e"},
				{token.SequenceEndType, "]\n"},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, toks(yamlscan.Tokenize(tc.input)))
		})
	}
}
