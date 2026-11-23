package tokens

import (
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/bom"
	"go.jacobcolvin.com/niceyaml/internal/lineend"
	"go.jacobcolvin.com/niceyaml/internal/reposition"
	"go.jacobcolvin.com/niceyaml/internal/yamlscan"
)

// Tokenize returns the token stream for the given YAML source. It reads
// the tokens with niceyaml's copy of the go-yaml scanner. The tokens that
// the go-yaml parser reads have the types and values that the go-yaml
// lexer gives them, except where the copy reads a construct as YAML
// does, and in the documents after an unclosed flow collection. Their
// Origins join to the source without its byte order
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
// # YAML Readings
//
// The copy reads some constructs as YAML reads them, where the go-yaml
// lexer reads them otherwise. So a decode of the stream of Tokenize reads
// these constructs as YAML does, and not as yaml.Unmarshal reads them:
//
//   - A tag that ends the source is a token, where the lexer drops it.
//     A decode reads "b: !!str" as an empty string, as it reads the same
//     text with a final line break. Only a space, a line break, or a ","
//     in a flow collection closes a tag, so in a file of "[!t]" the tag
//     "!t]" ends the source.
//   - The copy reads the rune after a double-quoted scalar that holds a
//     tab between its quotes. The lexer skips one rune after the closing
//     quote for each tab of the text that a rune other than a space or a
//     tab follows on its line, unless the closing quote comes right after
//     the tab. When the lexer skips a line break, it places each later
//     token on the line of the scalar.
//   - A tag without a value on its line does not type the first key of
//     the next line, nor the first key of the mapping below it. A tag
//     that go-yaml does not reserve, such as "!t", types the next token as
//     a string on its own line, as in "a: !t 0x10". The lexer types the
//     key below the tag as a string too, so it reads the key 0x10 below
//     "a: !t" as "0x10" and not as 16.
//
// A tab used as indentation makes the lexer read an invalid token. When a
// "-" or ":" indicator follows the tab, the invalid token holds it. The
// lexer does not make the invalid token on the line after a tag or after
// a comment that ends with a CRLF. Nor does it make one after a block
// scalar header that an invalid token ends. The copy keeps that reading
// of the lexer.
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
// The lexer does not emit a token for a source of whitespace alone.
// Tokenize gives such a source one token that holds the whole text, so
// the file stays visible. The token sits at line 1, column 1.
// [IsPlaceholder] tells that token apart from one the lexer makes.
//
// # Parser Input
//
// The first token that holds text opens with all the whitespace ahead of
// it, blank lines included. The go-yaml parser rejects a key whose Origin
// holds a blank line with a tab in front of its text, so hand the parser
// the stream [ForParser] returns.
func Tokenize(src string) token.Tokens {
	src, _ = bom.Drop(src)

	return yamlscan.Tokenize(src)
}

// IsPlaceholder reports whether tk is the token [Tokenize] made for a
// source of whitespace alone, which the lexer does not emit a token for.
// The parser reads that token as a plain scalar that holds the
// text. The go-yaml Unmarshal does not read a value from the text, so a
// decoder that wants to agree with Unmarshal treats the token as empty.
// Tokenize makes the placeholder as the only token of its stream, so a
// token linked to a neighbor, such as the content of a block scalar, is
// never one.
func IsPlaceholder(tk *token.Token) bool {
	if tk == nil || tk.Type != token.StringType || tk.Origin != tk.Value || tk.Prev != nil || tk.Next != nil {
		return false
	}

	return strings.Trim(tk.Origin, " \t\r\n") == ""
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
