package tokens

import (
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/bom"
	"go.jacobcolvin.com/niceyaml/internal/lineend"
	"go.jacobcolvin.com/niceyaml/internal/reposition"
	"go.jacobcolvin.com/niceyaml/internal/yamlscan"
)

// Tokenize returns the token stream for the given YAML source. It reads
// the tokens with niceyaml's copy of the go-yaml scanner. The tokens that
// the go-yaml parser reads have the types and values that the go-yaml
// lexer gives them, except in the documents after an unclosed flow
// collection. Their Origins join to the source without its byte order
// marks. Every token's Line, Column, and Offset count from 1 and name the
// rune of the joined Origins where its text starts. [ForParser] returns
// the stream to hand the go-yaml parser.
//
// # Origins
//
// Every token stream the module works with comes through Tokenize. The
// copy of the scanner keeps the text that the go-yaml lexer drops from
// the Origins. It keeps the spaces and tabs that end a line or fill a
// blank line. It keeps the whitespace in front of a ":" or "?" indicator.
// It keeps the letter and hex digits of a "\x", "\u", or "\U" escape in a
// double-quoted scalar. It ends the last token with the text that ends
// the source, such as the final line ending. It reads each line ending
// once. The lexer repeats the line ending after a tag at the start of the
// next token. The copy also leaves out the tokens that the lexer repeats.
// One is the end of the invalid token that the lexer makes of text after
// a block scalar header, and another is the tab that cuts a quoted scalar.
//
// The whitespace between the text of two tokens opens the Origin of the
// later token, as the go-yaml lexer places it. Where the lexer drops the
// spaces and tabs that end a line, the earlier token ends with the line
// breaks in front of the later token. The later token keeps the
// indentation of its own line. A CRLF stays whole in one Origin.
//
// Each tab between the text of a double-quoted scalar makes the lexer
// step one rune past the text that it reads, and the copy does the same.
// The Origin of a valid scalar ends with the runes after the scalar that
// the scan steps past. The invalid token of such a scalar, as when an
// escape in it ends too soon or a tab indents a later line, ends with the
// runes after the place where the scan stopped. The next token starts
// after those runes. When the last of those runes is the "\r" of a CRLF,
// the "\r" opens the Origin of the next token instead, so the CRLF stays
// whole.
//
// # Kept Readings
//
// The copy keeps the readings of the go-yaml lexer where a patch would
// change what the go-yaml parser reads. A tag that ends the source after
// other text is not a token, and its text ends the Origin of the last
// token. Only a space, a line break, or a "," in a flow collection closes
// a tag, so the "[" of a file of "[!t]" holds the whole file. A tag that
// go-yaml does not reserve, such as "!t", types the next token as a
// string. It does so even when the tag has no value on its line, where it
// types the first key of the next line.
//
// A tab used as indentation makes the lexer read an invalid token. When a
// "-" or ":" indicator follows the tab, the invalid token holds it.
//
// # Byte Order Marks
//
// Tokenize drops a UTF-8 byte order mark where YAML allows one: at the
// start of a line before the content of a document, and in front of a
// "---" or "..." marker. Otherwise the lexer reads the mark as text and
// puts it in the first key. Every position names a rune of the text
// without those marks.
//
// # Unclosed Flow Collections
//
// The go-yaml lexer counts the flow collections it has open, and a "---"
// or "..." marker does not reset the counts. After an unclosed "{" or
// "[", the lexer reads every later document as the inside of a flow
// collection, where a "," splits a plain scalar. The copy closes every
// open flow collection at a marker, so each document after it reads as it
// does in a source that starts at the marker.
//
// # Positions
//
// Every token's Line, Column, and Offset name the rune of the joined
// Origins where its text starts, which is the rune of the source without
// its byte order marks. Lines, columns, and offsets count
// from 1, and offsets count runes. The Origin can open with whitespace
// and line endings before that rune. The text of a token is its Origin
// without the whitespace around it. For a token cut across lines, such as
// a block scalar, the position names the first line that holds text.
//
// A token whose Origin does not hold text, such as the empty content of a
// block scalar, sits where the next text starts. It thus shares the
// position of the token after it. When text does not follow it, the token
// sits at the end of the last line.
//
// The lexer does not emit a token for a source of whitespace alone, nor
// for some text it rejects outright, such as a tag alone. Tokenize gives
// such a source one token that holds the whole text, so the file stays
// visible. The token sits where its text starts, and at line 1, column 1
// when the source holds whitespace alone. [IsPlaceholder] tells that
// token apart from one the lexer makes.
//
// # Parser Input
//
// The first token that holds text opens with all the whitespace ahead of
// it, blank lines included. The go-yaml parser rejects a key whose Origin
// holds a blank line with a tab in front of its text, so hand the parser
// the stream [ForParser] returns.
func Tokenize(src string) token.Tokens {
	src, _ = bom.Drop(src)

	return yamlscan.Tokenize(src, yamlscan.WithCompat(true))
}

// IsPlaceholder reports whether tk is the token [Tokenize] made for text
// that the lexer does not emit a token for, such as a source of whitespace
// alone. The parser reads that token as a plain scalar that holds the
// text. The go-yaml Unmarshal does not read a value from the text, so a
// decoder that wants to agree with Unmarshal treats the token as empty.
// Tokenize makes the placeholder as the only token of its stream, so a
// token linked to a neighbor, such as the content of a block scalar, is
// never one.
func IsPlaceholder(tk *token.Token) bool {
	if tk == nil || tk.Type != token.StringType || tk.Origin != tk.Value || tk.Prev != nil || tk.Next != nil {
		return false
	}

	return len(lexer.Tokenize(tk.Origin)) == 0
}

// ForParser returns clones of tks for the go-yaml parser to read.
//
// [Tokenize] gives the first token that holds text all the whitespace
// ahead of that text, blank lines included. The lexer keeps only the line
// breaks of the blank lines and the indentation of the last line. The
// parser trims spaces and line breaks from the start of a key's Origin and
// rejects the key when a line break remains. A blank line that holds a tab
// in front of the first key therefore makes the parser reject valid YAML.
// In the clone of that token, each blank line in front of the text keeps
// only its line break, and the indentation of the line the text starts on
// stays. Every other clone keeps the Origin of its token.
//
// Tokenize puts the content of a block scalar that holds whitespace alone
// where the next text starts. When that text is a comment, the parser
// reads the comment as sharing a line with the scalar and attaches it
// there. The clone of that content moves to where the token before it
// ends, on the line after the header, as the lexer places it. An empty
// content, such as that of a header without lines after it, keeps its
// position, as does every other clone.
//
// The parser relinks Next and Prev when it moves comment tokens, and the
// clones take that change in place of tks. The clones link to each other
// and not to any token outside the result. ForParser drops nil tokens.
//
// The parser builds from the clones the tree that go-yaml builds from the
// text, not the tree of [go.jacobcolvin.com/niceyaml.Source.File]. That
// method leaves out comments where the parser rejects them, such as a
// comment below a flow collection at the root of a document. The parser
// rejects a stream with such a comment, but Source.File accepts its text.
func ForParser(tks token.Tokens) token.Tokens {
	result := make(token.Tokens, 0, len(tks))

	shaped := false

	// The last clone that is not a comment. A clone of whitespace alone
	// that follows a block scalar header, with only comments between
	// them, is the content of that scalar.
	var lastBody *token.Token

	for _, clone := range clones(tks) {
		if !shaped && strings.Trim(clone.Origin, " \t\r\n") != "" {
			clone.Origin = bareLeadingLines(clone.Origin)
			shaped = true
		}

		if blockHeader(lastBody) && clone.Origin != "" && strings.Trim(clone.Origin, " \t\r\n") == "" {
			placeAfter(clone, result[len(result)-1])
		}

		if clone.Type != token.CommentType {
			lastBody = clone
		}

		result.Add(clone)
	}

	// Clone copies Next and Prev, and Add rewires only the links between
	// clones, so sever the links the ends keep to tokens outside tks.
	if len(result) > 0 {
		result[0].Prev = nil
		result[len(result)-1].Next = nil
	}

	return result
}

// clones returns a clone of each token of tks that is not nil, in order,
// as [token.Token.Clone] makes it: a copy of the token with a copy of its
// position. The clones and their positions take one allocation each in all,
// however many tokens tks holds.
func clones(tks token.Tokens) []*token.Token {
	n, positions := 0, 0

	for _, tk := range tks {
		if tk != nil {
			n++

			if tk.Position != nil {
				positions++
			}
		}
	}

	copies := make([]token.Token, n)
	posCopies := make([]token.Position, positions)
	out := make([]*token.Token, 0, n)

	for _, tk := range tks {
		if tk == nil {
			continue
		}

		c := &copies[len(out)]
		*c = *tk

		if tk.Position != nil {
			posCopies[0] = *tk.Position
			c.Position = &posCopies[0]
			posCopies = posCopies[1:]
		}

		out = append(out, c)
	}

	return out
}

// blockHeader reports whether tk is the header of a literal or folded
// block scalar.
func blockHeader(tk *token.Token) bool {
	return tk != nil && (tk.Type == token.LiteralType || tk.Type == token.FoldedType)
}

// placeAfter moves tk to the rune that follows the text of prev. It counts
// from the rune that the position of prev names.
func placeAfter(tk, prev *token.Token) {
	if tk.Position == nil || prev.Position == nil {
		return
	}

	rest := strings.TrimLeft(prev.Origin, " \t\r\n")

	tk.Position.Line, tk.Position.Column = lineend.Advance(prev.Position.Line, prev.Position.Column, rest)
	tk.Position.Offset = prev.Position.Offset + utf8.RuneCountInString(rest)
}

// bareLeadingLines returns origin with each blank line in front of its
// text cut down to its line break. The indentation of the line the text
// starts on stays. An Origin that does not hold text comes back as it is.
func bareLeadingLines(origin string) string {
	text := strings.TrimLeft(origin, " \t\r\n")
	if text == "" {
		return origin
	}

	lead := origin[:len(origin)-len(text)]
	cut := strings.LastIndexAny(lead, "\r\n") + 1

	breaks := strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return r
		}

		return -1
	}, lead[:cut])

	return breaks + lead[cut:] + text
}

// TrimLineEnding returns s without its trailing line ending: "\n", "\r\n",
// or a bare "\r".
func TrimLineEnding(s string) string {
	return lineend.Trim(s)
}

// ResetPositions clones tks and shifts the positions of the clones. A
// stream cut from a longer one, such as the tokens of one document from
// [go.jacobcolvin.com/niceyaml.Node.Tokens], then counts its lines from 1
// as [Tokenize] does. Every token moves by the same number of lines and
// the same offset distance, and tokens on the first line also move by the
// same number of columns. A whole stream from [Tokenize] comes back as
// clones with the same positions.
//
// The clones keep the invariant of [Tokenize]. Line, Column, and Offset
// name the rune where the token's text starts, and the Origin can open
// with whitespace and line endings before it.
//
// The first token that has a non-nil position and holds something other
// than whitespace anchors the shift. A fresh tokenize places that token
// after all the whitespace and line breaks ahead of its text. Earlier
// tokens can hold some of that whitespace alone, such as the invalid token
// the lexer emits for a tab that indents the line after "...". The
// anchor's own Origin can open with the rest, such as the line break that
// ends a preceding "..." line.
//
// The anchor lands at line 1, column 1, offset 1, where the lexer places
// the first token of a fresh stream, only when its text opens the joined
// Origins. A token whose Origin is empty, such as the empty content of a
// block scalar, shares the position of the text after it. It therefore
// anchors the shift only when none of the tokens of the stream holds text.
// The tokens of a stream that carries whitespace alone move to line 1,
// column 1, offset 1. There [Tokenize] places the token it makes of such
// text, ahead of the whitespace rather than inside it.
//
// A stream cut before the end of a longer one can end with tokens without
// text, such as the empty content of a block scalar that the next
// document's header closes. The last token of such a stream still links to
// a token after it. These tokens follow the last text of the clones, so
// they do not take the common shift. They move to the end of the last line
// of the joined Origins, before its final line ending, where [Tokenize]
// places them.
//
// The clones link to each other through Next and Prev and not to any token
// outside the result, so the stream stands alone. Tokens with nil
// positions come back as clones with nil positions, and ResetPositions
// drops nil tokens.
func ResetPositions(tks token.Tokens) token.Tokens {
	if len(tks) == 0 {
		return tks
	}

	return reposition.Reset(clones(tks))
}
