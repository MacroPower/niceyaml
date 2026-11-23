package yamlscan

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml/token"
)

// IndentState state for indent
type IndentState int

const (
	// IndentStateEqual equals previous indent
	IndentStateEqual IndentState = iota
	// IndentStateUp more indent than previous
	IndentStateUp
	// IndentStateDown less indent than previous
	IndentStateDown
	// IndentStateKeep uses not indent token
	IndentStateKeep
)

// Scanner holds the scanner's internal state while processing a given text.
// It can be allocated as part of another data structure but must be initialized via Init before use.
type Scanner struct {
	source     []rune
	sourcePos  int
	sourceSize int
	// line number. This number starts from 1.
	line int
	// column number. This number starts from 1.
	column int
	// offset represents the offset from the beginning of the source.
	offset int
	// lastDelimColumn is the last column needed to compare indent is retained.
	lastDelimColumn int
	// indentNum indicates the number of spaces used for indentation.
	indentNum int
	// prevLineIndentNum indicates the number of spaces used for indentation at previous line.
	prevLineIndentNum int
	// indentLevel indicates the level of indent depth. This value does not match the column value.
	indentLevel            int
	isFirstCharAtLine      bool
	isAnchor               bool
	isAlias                bool
	isDirective            bool
	startedFlowSequenceNum int
	startedFlowMapNum      int
	indentState            IndentState
	savedPos               *token.Position
	// niceyaml: rest holds the text that ends the input and that no token
	// of the last scan took, such as its final line ending.
	rest string
	// niceyaml: drops holds the rune index of each line break whose line
	// ends with spaces or tabs that upstream cuts from the origin.
	drops []int
	// niceyaml: heldLineEnd reports that the last token holds the line
	// ending that the scan reads next.
	heldLineEnd bool
	// niceyaml: headerLineEnd reports that the invalid token of a block
	// scalar header holds the line ending that upstream leaves for the
	// next scan.
	headerLineEnd bool
}

func (s *Scanner) pos() *token.Position {
	return &token.Position{
		Line:        s.line,
		Column:      s.column,
		Offset:      s.offset,
		IndentNum:   s.indentNum,
		IndentLevel: s.indentLevel,
	}
}

func (s *Scanner) bufferedToken(ctx *Context) *token.Token {
	if s.savedPos != nil {
		tk := ctx.bufferedToken(s.savedPos)
		s.savedPos = nil
		return tk
	}
	line := s.line
	column := s.column - len(ctx.buf)
	level := s.indentLevel
	if ctx.isMultiLine() {
		line -= s.newLineCount(ctx.buf)
		column = strings.Index(string(ctx.obuf), string(ctx.buf)) + 1
		// Since we are in a literal, folded or raw folded
		// we can use the indent level from the last token.
		last := ctx.lastToken()
		if last != nil { // The last token should never be nil here.
			level = last.Position.IndentLevel + 1
		}
	}
	return ctx.bufferedToken(&token.Position{
		Line:        line,
		Column:      column,
		Offset:      s.offset - len(ctx.buf),
		IndentNum:   s.indentNum,
		IndentLevel: level,
	})
}

func (s *Scanner) progressColumn(ctx *Context, num int) {
	s.column += num
	s.offset += num
	s.progress(ctx, num)
}

func (s *Scanner) progressOnly(ctx *Context, num int) {
	s.offset += num
	s.progress(ctx, num)
}

func (s *Scanner) progressLine(ctx *Context) {
	s.prevLineIndentNum = s.indentNum
	s.column = 1
	s.line++
	s.offset++
	s.indentNum = 0
	s.isFirstCharAtLine = true
	s.isAnchor = false
	s.isAlias = false
	s.isDirective = false
	s.progress(ctx, 1)
}

func (s *Scanner) progress(ctx *Context, num int) {
	ctx.progress(num)
	s.sourcePos += num
}

func (s *Scanner) isNewLineChar(c rune) bool {
	if c == '\n' {
		return true
	}
	if c == '\r' {
		return true
	}
	return false
}

func (s *Scanner) newLineCount(src []rune) int {
	size := len(src)
	cnt := 0
	for i := 0; i < size; i++ {
		c := src[i]
		switch c {
		case '\r':
			if i+1 < size && src[i+1] == '\n' {
				i++
			}
			cnt++
		case '\n':
			cnt++
		}
	}
	return cnt
}

func (s *Scanner) updateIndentLevel() {
	if s.prevLineIndentNum < s.indentNum {
		s.indentLevel++
	} else if s.prevLineIndentNum > s.indentNum {
		if s.indentLevel > 0 {
			s.indentLevel--
		}
	}
}

func (s *Scanner) updateIndentState(ctx *Context) {
	if s.lastDelimColumn == 0 {
		return
	}

	if s.lastDelimColumn < s.column {
		s.indentState = IndentStateUp
	} else {
		// If lastDelimColumn and s.column are the same,
		// treat as Down state since it is the same column as delimiter.
		s.indentState = IndentStateDown
	}
}

func (s *Scanner) updateIndent(ctx *Context, c rune) {
	if s.isFirstCharAtLine && s.isNewLineChar(c) {
		return
	}
	if s.isFirstCharAtLine && c == ' ' {
		s.indentNum++
		return
	}
	if s.isFirstCharAtLine && c == '\t' {
		// found tab indent.
		// In this case, scanTab returns error.
		return
	}
	if !s.isFirstCharAtLine {
		s.indentState = IndentStateKeep
		return
	}
	s.updateIndentLevel()
	s.updateIndentState(ctx)
	s.isFirstCharAtLine = false
}

func (s *Scanner) isChangedToIndentStateDown() bool {
	return s.indentState == IndentStateDown
}

func (s *Scanner) isChangedToIndentStateUp() bool {
	return s.indentState == IndentStateUp
}

func (s *Scanner) addBufferedTokenIfExists(ctx *Context) {
	ctx.addToken(s.bufferedToken(ctx))
}

func (s *Scanner) breakMultiLine(ctx *Context) {
	ctx.breakMultiLine()
}

func (s *Scanner) scanSingleQuote(ctx *Context) (*token.Token, error) {
	ctx.addOriginBuf('\'')
	srcpos := s.pos()
	startIndex := ctx.idx + 1
	src := ctx.src
	size := len(src)
	value := []rune{}
	isFirstLineChar := false
	isNewLine := false

	for idx := startIndex; idx < size; idx++ {
		if !isNewLine {
			s.progressColumn(ctx, 1)
		} else {
			isNewLine = false
		}
		c := src[idx]
		ctx.addOriginBuf(c)
		if s.isNewLineChar(c) {
			notSpaceIdx := -1
			for i := len(value) - 1; i >= 0; i-- {
				if value[i] == ' ' {
					continue
				}
				notSpaceIdx = i
				break
			}
			if len(value) > notSpaceIdx {
				value = value[:notSpaceIdx+1]
			}
			if isFirstLineChar {
				value = append(value, '\n')
			} else {
				value = append(value, ' ')
			}
			isFirstLineChar = true
			isNewLine = true
			s.progressLine(ctx)
			if idx+1 < size {
				if err := s.validateDocumentSeparatorMarker(ctx, src[idx+1:]); err != nil {
					return nil, err
				}
			}
			continue
		} else if isFirstLineChar && c == ' ' {
			continue
		} else if isFirstLineChar && c == '\t' {
			if s.lastDelimColumn >= s.column {
				invalidTk := token.Invalid(
					"tab character cannot be used for indentation in single-quoted text",
					string(ctx.obuf), s.pos(),
				)
				// niceyaml: the origin holds the tab, so the next scan
				// starts after it.
				s.progressColumn(ctx, 1)
				return nil, ErrInvalidToken(invalidTk)
			}
			continue
		} else if c != '\'' {
			value = append(value, c)
			isFirstLineChar = false
			continue
		} else if idx+1 < len(ctx.src) && ctx.src[idx+1] == '\'' {
			// '' handle as ' character
			value = append(value, c)
			ctx.addOriginBuf(c)
			idx++
			s.progressColumn(ctx, 1)
			continue
		}
		s.progressColumn(ctx, 1)
		return token.SingleQuote(string(value), string(ctx.obuf), srcpos), nil
	}
	s.progressColumn(ctx, 1)
	return nil, ErrInvalidToken(
		token.Invalid(
			"could not find end character of single-quoted text",
			string(ctx.obuf), srcpos,
		),
	)
}

func hexToInt(b rune) int {
	if b >= 'A' && b <= 'F' {
		return int(b) - 'A' + 10
	}
	if b >= 'a' && b <= 'f' {
		return int(b) - 'a' + 10
	}
	return int(b) - '0'
}

func hexRunesToInt(b []rune) int {
	sum := 0
	for i := 0; i < len(b); i++ {
		sum += hexToInt(b[i]) << (uint(len(b)-i-1) * 4)
	}
	return sum
}

func (s *Scanner) scanDoubleQuote(ctx *Context) (*token.Token, error) {
	ctx.addOriginBuf('"')
	srcpos := s.pos()
	startIndex := ctx.idx + 1
	src := ctx.src
	size := len(src)
	value := []rune{}
	isFirstLineChar := false
	isNewLine := false

	for idx := startIndex; idx < size; idx++ {
		if !isNewLine {
			s.progressColumn(ctx, 1)
		} else {
			isNewLine = false
		}
		c := src[idx]
		ctx.addOriginBuf(c)
		if s.isNewLineChar(c) {
			notSpaceIdx := -1
			for i := len(value) - 1; i >= 0; i-- {
				if value[i] == ' ' {
					continue
				}
				notSpaceIdx = i
				break
			}
			if len(value) > notSpaceIdx {
				value = value[:notSpaceIdx+1]
			}
			if isFirstLineChar {
				value = append(value, '\n')
			} else {
				value = append(value, ' ')
			}
			isFirstLineChar = true
			isNewLine = true
			s.progressLine(ctx)
			if idx+1 < size {
				if err := s.validateDocumentSeparatorMarker(ctx, src[idx+1:]); err != nil {
					return nil, err
				}
			}
			continue
		} else if isFirstLineChar && c == ' ' {
			continue
		} else if isFirstLineChar && c == '\t' {
			if s.lastDelimColumn >= s.column {
				invalidTk := token.Invalid(
					"tab character cannot be used for indentation in double-quoted text",
					string(ctx.obuf), s.pos(),
				)
				// niceyaml: the origin holds the tab, so the next scan
				// starts after it.
				s.progressColumn(ctx, 1)
				return nil, ErrInvalidToken(invalidTk)
			}
			continue
		} else if c == '\\' {
			isFirstLineChar = false
			if idx+1 >= size {
				value = append(value, c)
				continue
			}
			nextChar := src[idx+1]
			progress := 0
			switch nextChar {
			case '0':
				progress = 1
				ctx.addOriginBuf(nextChar)
				value = append(value, 0x00)
			case 'a':
				progress = 1
				ctx.addOriginBuf(nextChar)
				value = append(value, 0x07)
			case 'b':
				progress = 1
				ctx.addOriginBuf(nextChar)
				value = append(value, 0x08)
			case 't':
				progress = 1
				ctx.addOriginBuf(nextChar)
				value = append(value, 0x09)
			case 'n':
				progress = 1
				ctx.addOriginBuf(nextChar)
				value = append(value, 0x0A)
			case 'v':
				progress = 1
				ctx.addOriginBuf(nextChar)
				value = append(value, 0x0B)
			case 'f':
				progress = 1
				ctx.addOriginBuf(nextChar)
				value = append(value, 0x0C)
			case 'r':
				progress = 1
				ctx.addOriginBuf(nextChar)
				value = append(value, 0x0D)
			case 'e':
				progress = 1
				ctx.addOriginBuf(nextChar)
				value = append(value, 0x1B)
			case ' ':
				progress = 1
				ctx.addOriginBuf(nextChar)
				value = append(value, 0x20)
			case '"':
				progress = 1
				ctx.addOriginBuf(nextChar)
				value = append(value, 0x22)
			case '/':
				progress = 1
				ctx.addOriginBuf(nextChar)
				value = append(value, 0x2F)
			case '\\':
				progress = 1
				ctx.addOriginBuf(nextChar)
				value = append(value, 0x5C)
			case 'N':
				progress = 1
				ctx.addOriginBuf(nextChar)
				value = append(value, 0x85)
			case '_':
				progress = 1
				ctx.addOriginBuf(nextChar)
				value = append(value, 0xA0)
			case 'L':
				progress = 1
				ctx.addOriginBuf(nextChar)
				value = append(value, 0x2028)
			case 'P':
				progress = 1
				ctx.addOriginBuf(nextChar)
				value = append(value, 0x2029)
			case 'x':
				if idx+3 >= size {
					progress = 1
					ctx.addOriginBuf(nextChar)
					value = append(value, nextChar)
				} else {
					progress = 3
					codeNum := hexRunesToInt(src[idx+2 : idx+progress+1])
					value = append(value, rune(codeNum))
					ctx.addOriginRunes(src[idx+1 : idx+progress+1]) // niceyaml: keep the code of the escape.
				}
			case 'u':
				// \u0000 style must have 5 characters at least.
				if idx+5 >= size {
					// niceyaml: the next token holds the backslash.
					return nil, ErrInvalidToken(
						abandoned(token.Invalid(
							"not enough length for escaped UTF-16 character",
							string(ctx.obuf), s.pos(),
						)),
					)
				}
				progress = 5
				codeNum := hexRunesToInt(src[idx+2 : idx+6])

				// handle surrogate pairs.
				if codeNum >= 0xD800 && codeNum <= 0xDBFF {
					high := codeNum

					// \u0000\u0000 style must have 11 characters at least.
					if idx+11 >= size {
						// niceyaml: the next token holds the backslash.
						return nil, ErrInvalidToken(
							abandoned(token.Invalid(
								"not enough length for escaped UTF-16 surrogate pair",
								string(ctx.obuf), s.pos(),
							)),
						)
					}

					if src[idx+6] != '\\' || src[idx+7] != 'u' {
						// niceyaml: the next token holds the backslash.
						return nil, ErrInvalidToken(
							abandoned(token.Invalid(
								"found unexpected character after high surrogate for UTF-16 surrogate pair",
								string(ctx.obuf), s.pos(),
							)),
						)
					}

					low := hexRunesToInt(src[idx+8 : idx+12])
					if low < 0xDC00 || low > 0xDFFF {
						// niceyaml: the next token holds the backslash.
						return nil, ErrInvalidToken(
							abandoned(token.Invalid(
								"found unexpected low surrogate after high surrogate",
								string(ctx.obuf), s.pos(),
							)),
						)
					}
					codeNum = ((high - 0xD800) * 0x400) + (low - 0xDC00) + 0x10000
					progress += 6
				}
				value = append(value, rune(codeNum))
				ctx.addOriginRunes(src[idx+1 : idx+progress+1]) // niceyaml: keep the code of the escape.
			case 'U':
				// \U00000000 style must have 9 characters at least.
				if idx+9 >= size {
					// niceyaml: the next token holds the backslash.
					return nil, ErrInvalidToken(
						abandoned(token.Invalid(
							"not enough length for escaped UTF-32 character",
							string(ctx.obuf), s.pos(),
						)),
					)
				}
				progress = 9
				codeNum := hexRunesToInt(src[idx+2 : idx+10])
				value = append(value, rune(codeNum))
				ctx.addOriginRunes(src[idx+1 : idx+progress+1]) // niceyaml: keep the code of the escape.
			case '\n':
				isFirstLineChar = true
				isNewLine = true
				ctx.addOriginBuf(nextChar)
				s.progressColumn(ctx, 1)
				s.progressLine(ctx)
				idx++
				continue
			case '\r':
				isFirstLineChar = true
				isNewLine = true
				ctx.addOriginBuf(nextChar)
				s.progressLine(ctx)
				progress = 1
				// Skip \n after \r in CRLF sequences
				if idx+2 < size && src[idx+2] == '\n' {
					ctx.addOriginBuf('\n')
					progress = 2
				}
			case '\t':
				progress = 1
				ctx.addOriginBuf(nextChar)
				value = append(value, nextChar)
			default:
				s.progressColumn(ctx, 1)
				return nil, ErrInvalidToken(
					token.Invalid(
						fmt.Sprintf("found unknown escape character %q", nextChar),
						string(ctx.obuf), s.pos(),
					),
				)
			}
			idx += progress
			s.progressColumn(ctx, progress)
			continue
		} else if c == '\t' {
			var (
				foundNotSpaceChar bool
				progress          int
			)
			for i := idx + 1; i < size; i++ {
				if src[i] == ' ' || src[i] == '\t' {
					progress++
					continue
				}
				if s.isNewLineChar(src[i]) {
					break
				}
				foundNotSpaceChar = true
			}
			if foundNotSpaceChar {
				// niceyaml: upstream steps one more rune here, and that
				// step makes the scan skip the rune after the scalar.
				value = append(value, c)
			} else {
				// niceyaml: only spaces and tabs follow on the line, so
				// the loop counted all of them, and the origin keeps them.
				ctx.addOriginRunes(src[idx+1 : idx+1+progress])
				idx += progress
				s.progressColumn(ctx, progress)
			}
			continue
		} else if c != '"' {
			value = append(value, c)
			isFirstLineChar = false
			continue
		}
		s.progressColumn(ctx, 1)
		return token.DoubleQuote(string(value), string(ctx.obuf), srcpos), nil
	}
	s.progressColumn(ctx, 1)
	return nil, ErrInvalidToken(
		token.Invalid(
			"could not find end character of double-quoted text",
			string(ctx.obuf), srcpos,
		),
	)
}

func (s *Scanner) validateDocumentSeparatorMarker(ctx *Context, src []rune) error {
	if s.foundDocumentSeparatorMarker(src) {
		return ErrInvalidToken(
			token.Invalid("found unexpected document separator", string(ctx.obuf), s.pos()),
		)
	}
	return nil
}

func (s *Scanner) foundDocumentSeparatorMarker(src []rune) bool {
	if len(src) < 3 {
		return false
	}
	var marker string
	if len(src) == 3 {
		marker = string(src)
	} else {
		marker = strings.TrimRightFunc(string(src[:4]), func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == '\r'
		})
	}
	return marker == "---" || marker == "..."
}

func (s *Scanner) scanQuote(ctx *Context, ch rune) (bool, error) {
	if ctx.existsBuffer() {
		return false, nil
	}
	if ch == '\'' {
		tk, err := s.scanSingleQuote(ctx)
		if err != nil {
			return false, err
		}
		ctx.addToken(tk)
	} else {
		tk, err := s.scanDoubleQuote(ctx)
		if err != nil {
			return false, err
		}
		ctx.addToken(tk)
	}
	ctx.clear()
	return true, nil
}

func (s *Scanner) scanWhiteSpace(ctx *Context) bool {
	if ctx.isMultiLine() {
		return false
	}
	if !s.isAnchor && !s.isDirective && !s.isAlias && !s.isFirstCharAtLine {
		return false
	}

	if s.isFirstCharAtLine {
		s.progressColumn(ctx, 1)
		ctx.addOriginBuf(' ')
		return true
	}
	if s.isDirective {
		s.addBufferedTokenIfExists(ctx)
		s.progressColumn(ctx, 1)
		ctx.addOriginBuf(' ')
		return true
	}

	// niceyaml: the name token takes the spaces and tabs after the name
	// through the last tab, as after a tab. When the blanks run to a line
	// break, upstream gives the name token the blanks and the line break.
	if s.inName(ctx) && !blankRunEndsLine(ctx, ctx.idx) {
		s.closeName(ctx)
		return true
	}

	s.addBufferedTokenIfExists(ctx)
	s.isAnchor = false
	s.isAlias = false
	return true
}

func (s *Scanner) isMergeKey(ctx *Context) bool {
	if ctx.repeatNum('<') != 2 {
		return false
	}
	src := ctx.src
	size := len(src)
	for idx := ctx.idx + 2; idx < size; idx++ {
		c := src[idx]
		if c == ' ' {
			continue
		}
		if c != ':' {
			return false
		}
		if idx+1 < size {
			nc := src[idx+1]
			if nc == ' ' || s.isNewLineChar(nc) {
				return true
			}
		}
	}
	return false
}

func (s *Scanner) scanTag(ctx *Context) (bool, error) {
	if ctx.existsBuffer() || s.isDirective {
		return false, nil
	}

	ctx.addOriginBuf('!')
	s.progress(ctx, 1) // skip '!' character

	var progress int
	for idx, c := range ctx.src[ctx.idx:] {
		progress = idx + 1
		switch c {
		// niceyaml: a tab closes the tag too, where upstream reads the tab
		// and the text after it into the tag. The tag takes the spaces and
		// tabs after it through the last tab, unless they run to a line
		// break. So the origin of the next token does not open with a tab.
		case ' ', '\t':
			blank := ctx.idx + idx
			end := blank + 1
			if !blankRunEndsLine(ctx, blank) {
				end = max(end, blankRunTabEnd(ctx, blank))
			}
			for _, b := range ctx.src[blank:end] {
				ctx.addOriginBuf(b)
			}
			value := ctx.source(ctx.idx-1, blank)
			ctx.addToken(token.Tag(value, string(ctx.obuf), s.pos()))
			s.progressColumn(ctx, len([]rune(value))+end-blank-1)
			ctx.clear()
			return true, nil
		case ',':
			if s.startedFlowSequenceNum > 0 || s.startedFlowMapNum > 0 {
				value := ctx.source(ctx.idx-1, ctx.idx+idx)
				ctx.addToken(token.Tag(value, string(ctx.obuf), s.pos()))
				s.progressColumn(ctx, len([]rune(value))-1) // progress column before collect-entry for scanning it at scanFlowEntry function.
				ctx.clear()
				return true, nil
			} else {
				ctx.addOriginBuf(c)
			}
		case '\n', '\r':
			// niceyaml: the tag takes its whole line ending, and the scan
			// of that line ending leaves it out of the next origin.
			ctx.addOriginBuf(c)
			if c == '\r' && ctx.idx+idx+1 < ctx.size && ctx.src[ctx.idx+idx+1] == '\n' {
				ctx.addOriginBuf('\n')
			}
			s.heldLineEnd = true
			value := ctx.source(ctx.idx-1, ctx.idx+idx)
			ctx.addToken(token.Tag(value, string(ctx.obuf), s.pos()))
			s.progressColumn(ctx, len([]rune(value))-1) // progress column before new-line-char for scanning new-line-char at scanNewLine function.
			ctx.clear()
			return true, nil
		case '{', '}':
			ctx.addOriginBuf(c)
			s.progressColumn(ctx, progress)
			invalidTk := token.Invalid(fmt.Sprintf("found invalid tag character %q", c), string(ctx.obuf), s.pos())
			return false, ErrInvalidToken(invalidTk)
		default:
			ctx.addOriginBuf(c)
		}
	}
	// niceyaml: a tag that ends the input is a token too, where upstream
	// drops it.
	value := ctx.source(ctx.idx-1, ctx.size)
	ctx.addToken(token.Tag(value, string(ctx.obuf), s.pos()))
	s.progressColumn(ctx, progress)
	ctx.clear()
	return true, nil
}

func (s *Scanner) scanComment(ctx *Context) bool {
	if ctx.existsBuffer() {
		c := ctx.previousChar()
		if c != ' ' && c != '\t' && !s.isNewLineChar(c) {
			return false
		}
	}

	s.addBufferedTokenIfExists(ctx)
	ctx.addOriginBuf('#')
	s.progress(ctx, 1) // skip '#' character

	for idx, c := range ctx.src[ctx.idx:] {
		ctx.addOriginBuf(c)
		if !s.isNewLineChar(c) {
			continue
		}
		if ctx.previousChar() == '\\' {
			continue
		}
		value := ctx.source(ctx.idx, ctx.idx+idx)
		progress := len([]rune(value))
		// niceyaml: a CRLF ends the comment whole. Upstream ends the
		// comment with the "\r" and counts the "\n" as a second line.
		crlf := c == '\r' && ctx.idx+idx+1 < ctx.size && ctx.src[ctx.idx+idx+1] == '\n'
		if crlf {
			ctx.addOriginBuf('\n')
		}
		ctx.addToken(token.Comment(value, string(ctx.obuf), s.pos()))
		s.progressColumn(ctx, progress)
		if crlf {
			s.progress(ctx, 1)
			s.offset++
		}
		s.progressLine(ctx)
		ctx.clear()
		// niceyaml: the comment holds the "\n" of the CRLF, which opens
		// the next origin buffer upstream.
		ctx.lineBefore = crlf
		return true
	}
	// document ends with comment.
	value := string(ctx.src[ctx.idx:])
	ctx.addToken(token.Comment(value, string(ctx.obuf), s.pos()))
	progress := len([]rune(value))
	s.progressColumn(ctx, progress)
	s.progressLine(ctx)
	ctx.clear()
	return true
}

func (s *Scanner) scanMultiLine(ctx *Context, c rune) error {
	state := ctx.getMultiLineState()
	ctx.addOriginBuf(c)
	// normalize CR and CRLF to LF
	if c == '\r' {
		if ctx.nextChar() == '\n' {
			ctx.addOriginBuf('\n')
			s.progress(ctx, 1)
			s.offset++
		}
		c = '\n'
	}
	if ctx.isEOS() {
		if s.isFirstCharAtLine && c == ' ' {
			state.addIndent(ctx, s.column)
		} else {
			ctx.addBuf(c)
		}
		state.updateIndentColumn(s.column)
		if err := state.validateIndentColumn(); err != nil {
			invalidTk := token.Invalid(err.Error(), string(ctx.obuf), s.pos())
			s.progressColumn(ctx, 1)
			return ErrInvalidToken(invalidTk)
		}
		value := ctx.bufferedSrc()
		ctx.addToken(token.String(string(value), string(ctx.obuf), s.pos()))
		ctx.clear()
		s.progressColumn(ctx, 1)
	} else if s.isNewLineChar(c) {
		ctx.addBuf(c)
		state.updateSpaceOnlyIndentColumn(s.column - 1)
		state.updateNewLineState()
		s.progressLine(ctx)
		if ctx.next() {
			if s.foundDocumentSeparatorMarker(ctx.src[ctx.idx:]) {
				value := ctx.bufferedSrc()
				ctx.addToken(token.String(string(value), string(ctx.obuf), s.pos()))
				ctx.clear()
				s.breakMultiLine(ctx)
			}
		}
	} else if s.isFirstCharAtLine && c == ' ' {
		state.addIndent(ctx, s.column)
		s.progressColumn(ctx, 1)
	} else if s.isFirstCharAtLine && c == '\t' && state.isIndentColumn(s.column) {
		err := ErrInvalidToken(
			token.Invalid(
				"found a tab character where an indentation space is expected",
				string(ctx.obuf), s.pos(),
			),
		)
		s.progressColumn(ctx, 1)
		return err
	} else if c == '\t' && !state.isIndentColumn(s.column) {
		ctx.addBufWithTab(c)
		s.progressColumn(ctx, 1)
	} else {
		if err := state.validateIndentAfterSpaceOnly(s.column); err != nil {
			invalidTk := token.Invalid(err.Error(), string(ctx.obuf), s.pos())
			s.progressColumn(ctx, 1)
			return ErrInvalidToken(invalidTk)
		}
		state.updateIndentColumn(s.column)
		if err := state.validateIndentColumn(); err != nil {
			invalidTk := token.Invalid(err.Error(), string(ctx.obuf), s.pos())
			s.progressColumn(ctx, 1)
			return ErrInvalidToken(invalidTk)
		}
		if col := state.lastDelimColumn(); col > 0 {
			s.lastDelimColumn = col
		}
		state.updateNewLineInFolded(ctx, s.column)
		ctx.addBufWithTab(c)
		s.progressColumn(ctx, 1)
	}
	return nil
}

func (s *Scanner) scanNewLine(ctx *Context, c rune) {
	if len(ctx.buf) > 0 && s.savedPos == nil {
		bufLen := len(ctx.bufferedSrc())
		s.savedPos = s.pos()
		s.savedPos.Column -= bufLen
		s.savedPos.Offset -= bufLen
	}

	// if the following case, origin buffer has unnecessary two spaces.
	// So, `removeRightSpaceFromOriginBuf` remove them, also fix column number too.
	// ---
	// a:[space][space]
	//   b: c
	// niceyaml: record the line break whose line ends with spaces or
	// tabs, so the settle pass can place them.
	if ctx.removeRightSpaceFromBuf() {
		s.drops = append(s.drops, s.sourcePos)
	}

	// There is no problem that we ignore CR which followed by LF and normalize it to LF, because of following YAML1.2 spec.
	// > Line breaks inside scalar content must be normalized by the YAML processor. Each such line break must be parsed into a single line feed character.
	// > Outside scalar content, YAML allows any line break to be used to terminate lines.
	// > -- https://yaml.org/spec/1.2/spec.html
	// niceyaml: the "\r" of a CRLF joins the origin after the token that
	// the line ending cuts, so the CRLF stays whole in one origin.
	crlf := c == '\r' && ctx.nextChar() == '\n'
	if crlf {
		s.progress(ctx, 1)
		s.offset++
		c = '\n'
	}

	if ctx.isEOS() {
		s.addBufferedTokenIfExists(ctx)
	} else if s.isAnchor || s.isAlias || s.isDirective {
		s.addBufferedTokenIfExists(ctx)
	}
	if crlf {
		ctx.addOriginBuf('\r')
	}
	if ctx.existsBuffer() && s.isFirstCharAtLine {
		if ctx.buf[len(ctx.buf)-1] == ' ' {
			ctx.buf[len(ctx.buf)-1] = '\n'
		} else {
			ctx.buf = append(ctx.buf, '\n')
		}
	} else {
		ctx.addBuf(' ')
	}
	ctx.addOriginBuf(c)
	s.progressLine(ctx)
	// niceyaml: a line break follows the tag that is the last token.
	if tk := ctx.lastToken(); tk != nil && tk.Type == token.TagType {
		ctx.tagEndedLine = true
	}
}

func (s *Scanner) isFlowMode() bool {
	if s.startedFlowSequenceNum > 0 {
		return true
	}
	if s.startedFlowMapNum > 0 {
		return true
	}
	return false
}

func (s *Scanner) scanFlowMapStart(ctx *Context) bool {
	if ctx.existsBuffer() && !s.isFlowMode() {
		return false
	}

	s.addBufferedTokenIfExists(ctx)
	ctx.addOriginBuf('{')
	ctx.addToken(token.MappingStart(string(ctx.obuf), s.pos()))
	s.startedFlowMapNum++
	s.progressColumn(ctx, 1)
	ctx.clear()
	return true
}

func (s *Scanner) scanFlowMapEnd(ctx *Context) bool {
	if s.startedFlowMapNum <= 0 {
		return false
	}

	s.addBufferedTokenIfExists(ctx)
	ctx.addOriginBuf('}')
	ctx.addToken(token.MappingEnd(string(ctx.obuf), s.pos()))
	s.startedFlowMapNum--
	s.progressColumn(ctx, 1)
	ctx.clear()
	return true
}

func (s *Scanner) scanFlowArrayStart(ctx *Context) bool {
	if ctx.existsBuffer() && !s.isFlowMode() {
		return false
	}

	s.addBufferedTokenIfExists(ctx)
	ctx.addOriginBuf('[')
	ctx.addToken(token.SequenceStart(string(ctx.obuf), s.pos()))
	s.startedFlowSequenceNum++
	s.progressColumn(ctx, 1)
	ctx.clear()
	return true
}

func (s *Scanner) scanFlowArrayEnd(ctx *Context) bool {
	if ctx.existsBuffer() && s.startedFlowSequenceNum <= 0 {
		return false
	}

	s.addBufferedTokenIfExists(ctx)
	ctx.addOriginBuf(']')
	ctx.addToken(token.SequenceEnd(string(ctx.obuf), s.pos()))
	s.startedFlowSequenceNum--
	s.progressColumn(ctx, 1)
	ctx.clear()
	return true
}

func (s *Scanner) scanFlowEntry(ctx *Context, c rune) bool {
	if s.startedFlowSequenceNum <= 0 && s.startedFlowMapNum <= 0 {
		return false
	}

	s.addBufferedTokenIfExists(ctx)
	ctx.addOriginBuf(c)
	ctx.addToken(token.CollectEntry(string(ctx.obuf), s.pos()))
	s.progressColumn(ctx, 1)
	ctx.clear()
	return true
}

func (s *Scanner) scanMapDelim(ctx *Context) (bool, error) {
	nc := ctx.nextChar()
	if s.isDirective || s.isAnchor || s.isAlias {
		return false, nil
	}
	if s.startedFlowMapNum <= 0 && nc != ' ' && nc != '\t' && !s.isNewLineChar(nc) && !ctx.isNextEOS() {
		return false, nil
	}
	if s.startedFlowMapNum > 0 && nc == '/' {
		// like http://
		return false, nil
	}
	if s.startedFlowMapNum > 0 {
		tk := ctx.lastToken()
		if tk != nil && tk.Type == token.MappingValueType {
			return false, nil
		}
	}

	if ctx.tabIndented() && !strings.HasPrefix(string(ctx.buf), "\t") {
		// niceyaml: the scan steps over the ":", so the origin holds it.
		invalidTk := token.Invalid("tab character cannot use as a map key directly", string(ctx.obuf), s.pos())
		invalidTk.Origin += ":"
		s.progressColumn(ctx, 1)
		return false, ErrInvalidToken(invalidTk)
	}

	// mapping value
	// niceyaml: a tag on a line before this key does not type the key.
	if ctx.tagEndedLine {
		ctx.tagClosed = true
	}
	tk := s.bufferedToken(ctx)
	if tk != nil {
		s.lastDelimColumn = tk.Position.Column
		ctx.addToken(tk)
	} else if tk := ctx.lastToken(); tk != nil {
		// If the map key is quote, the buffer does not exist because it has already been cut into tokens.
		// Therefore, we need to check the last token.
		if tk.Indicator == token.QuotedScalarIndicator {
			s.lastDelimColumn = tk.Position.Column
		}
	}
	mv := token.MappingValue(s.pos())
	mv.Origin = string(ctx.obuf) + mv.Origin // niceyaml: keep the whitespace in front of the ":".
	ctx.addToken(mv)
	s.progressColumn(ctx, 1)
	ctx.clear()
	return true, nil
}

func (s *Scanner) scanDocumentStart(ctx *Context) bool {
	if s.indentNum != 0 {
		return false
	}
	if s.column != 1 {
		return false
	}
	if ctx.repeatNum('-') != 3 {
		return false
	}
	if ctx.size > ctx.idx+3 {
		c := ctx.src[ctx.idx+3]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return false
		}
	}

	s.addBufferedTokenIfExists(ctx)
	ctx.addToken(token.DocumentHeader(string(ctx.obuf)+"---", s.pos()))
	s.progressColumn(ctx, 3)
	ctx.clear()
	s.clearState()
	s.closeFlows() // niceyaml: close the flow collections that the document left open.
	return true
}

func (s *Scanner) scanDocumentEnd(ctx *Context) bool {
	if s.indentNum != 0 {
		return false
	}
	if s.column != 1 {
		return false
	}
	if ctx.repeatNum('.') != 3 {
		return false
	}

	s.addBufferedTokenIfExists(ctx)
	ctx.addToken(token.DocumentEnd(string(ctx.obuf)+"...", s.pos()))
	s.progressColumn(ctx, 3)
	ctx.clear()
	// niceyaml: close the flow collections that the document left open,
	// and clear the indent state that a flow collection left behind.
	if s.isFlowMode() || s.startedFlowSequenceNum < 0 {
		s.clearState()
	}
	s.closeFlows()
	return true
}

func (s *Scanner) scanMergeKey(ctx *Context) bool {
	if !s.isMergeKey(ctx) {
		return false
	}

	s.lastDelimColumn = s.column
	ctx.addToken(token.MergeKey(string(ctx.obuf)+"<<", s.pos()))
	s.progressColumn(ctx, 2)
	ctx.clear()
	return true
}

func (s *Scanner) scanRawFoldedChar(ctx *Context) bool {
	if !ctx.existsBuffer() {
		return false
	}
	if !s.isChangedToIndentStateUp() {
		return false
	}

	ctx.setRawFolded(s.column)
	ctx.addBuf('-')
	ctx.addOriginBuf('-')
	s.progressColumn(ctx, 1)
	return true
}

func (s *Scanner) scanSequence(ctx *Context) (bool, error) {
	if ctx.existsBuffer() {
		return false, nil
	}

	nc := ctx.nextChar()
	if nc != 0 && nc != ' ' && nc != '\t' && !s.isNewLineChar(nc) {
		return false, nil
	}

	if ctx.tabIndented() {
		// niceyaml: the scan steps over the "-", so the origin holds it.
		invalidTk := token.Invalid("tab character cannot use as a sequence delimiter", string(ctx.obuf), s.pos())
		invalidTk.Origin += "-"
		s.progressColumn(ctx, 1)
		return false, ErrInvalidToken(invalidTk)
	}

	s.addBufferedTokenIfExists(ctx)
	ctx.addOriginBuf('-')
	tk := token.SequenceEntry(string(ctx.obuf), s.pos())
	s.lastDelimColumn = tk.Position.Column
	ctx.addToken(tk)
	s.progressColumn(ctx, 1)
	ctx.clear()
	return true, nil
}

func (s *Scanner) scanMultiLineHeader(ctx *Context) (bool, error) {
	if ctx.existsBuffer() {
		return false, nil
	}

	if err := s.scanMultiLineHeaderOption(ctx); err != nil {
		return false, err
	}
	s.progressLine(ctx)
	return true, nil
}

func (s *Scanner) validateMultiLineHeaderOption(opt string) error {
	if len(opt) == 0 {
		return nil
	}
	orgOpt := opt
	opt = strings.TrimPrefix(opt, "-")
	opt = strings.TrimPrefix(opt, "+")
	opt = strings.TrimSuffix(opt, "-")
	opt = strings.TrimSuffix(opt, "+")
	if len(opt) == 0 {
		return nil
	}
	if opt == "0" {
		return fmt.Errorf("invalid header option: %q", orgOpt)
	}
	i, err := strconv.ParseInt(opt, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid header option: %q", orgOpt)
	}
	if i > 9 {
		return fmt.Errorf("invalid header option: %q", orgOpt)
	}
	return nil
}

func (s *Scanner) scanMultiLineHeaderOption(ctx *Context) error {
	header := ctx.currentChar()
	ctx.addOriginBuf(header)
	s.progress(ctx, 1) // skip '|' or '>' character

	var progress int
	var crlf bool
	var ended bool // niceyaml: whether a line ending closes the header.
	for idx, c := range ctx.src[ctx.idx:] {
		progress = idx
		ctx.addOriginBuf(c)
		if s.isNewLineChar(c) {
			nextIdx := ctx.idx + idx + 1
			if c == '\r' && nextIdx < len(ctx.src) && ctx.src[nextIdx] == '\n' {
				crlf = true
				continue // process \n in the next iteration
			}
			ended = true // niceyaml: a line ending closes the header.
			break
		}
	}
	endPos := ctx.idx + progress
	if crlf {
		// Exclude \r
		endPos = endPos - 1
	}
	value := strings.TrimRight(ctx.source(ctx.idx, endPos), " ")
	commentValueIndex := strings.Index(value, "#")
	opt := value
	if commentValueIndex > 0 {
		opt = value[:commentValueIndex]
	}
	opt = strings.TrimRightFunc(opt, func(r rune) bool {
		return r == ' ' || r == '\t'
	})
	if len(opt) != 0 {
		if err := s.validateMultiLineHeaderOption(opt); err != nil {
			invalidTk := token.Invalid(err.Error(), string(ctx.obuf), s.pos())
			s.progressColumn(ctx, progress)
			// niceyaml: the invalid token holds the whole line, so the next
			// scan starts after it and repeats none of it.
			if ended {
				s.progressLine(ctx)
				s.headerLineEnd = true
			} else {
				s.progressColumn(ctx, 1)
			}
			return ErrInvalidToken(invalidTk)
		}
	}
	if s.column == 1 {
		s.lastDelimColumn = 1
	}

	commentIndex := strings.Index(string(ctx.obuf), "#")
	headerBuf := string(ctx.obuf)
	if commentIndex > 0 {
		headerBuf = headerBuf[:commentIndex]
	}
	switch header {
	case '|':
		ctx.addToken(token.Literal("|"+opt, headerBuf, s.pos()))
		ctx.setLiteral(s.lastDelimColumn, opt)
	case '>':
		ctx.addToken(token.Folded(">"+opt, headerBuf, s.pos()))
		ctx.setFolded(s.lastDelimColumn, opt)
	}
	if commentIndex > 0 {
		comment := value[commentValueIndex+1:]
		s.offset += len(headerBuf)
		s.column += len(headerBuf)
		ctx.addToken(token.Comment(comment, string(ctx.obuf[len(headerBuf):]), s.pos()))
	}
	s.indentState = IndentStateKeep
	ctx.resetBuffer()
	s.progressColumn(ctx, progress)
	return nil
}

func (s *Scanner) scanMapKey(ctx *Context) bool {
	if ctx.existsBuffer() {
		return false
	}

	nc := ctx.nextChar()
	if nc != ' ' && nc != '\t' {
		return false
	}

	tk := token.MappingKey(s.pos())
	tk.Origin = string(ctx.obuf) + tk.Origin // niceyaml: keep the whitespace in front of the "?".
	s.lastDelimColumn = tk.Position.Column
	ctx.addToken(tk)
	s.progressColumn(ctx, 1)
	ctx.clear()
	return true
}

func (s *Scanner) scanDirective(ctx *Context) bool {
	if ctx.existsBuffer() {
		return false
	}
	if s.indentNum != 0 {
		return false
	}

	s.addBufferedTokenIfExists(ctx)
	ctx.addOriginBuf('%')
	ctx.addToken(token.Directive(string(ctx.obuf), s.pos()))
	s.progressColumn(ctx, 1)
	ctx.clear()
	s.isDirective = true
	return true
}

func (s *Scanner) scanAnchor(ctx *Context) bool {
	if ctx.existsBuffer() {
		return false
	}

	s.addBufferedTokenIfExists(ctx)
	ctx.addOriginBuf('&')
	ctx.addToken(token.Anchor(string(ctx.obuf), s.pos()))
	s.progressColumn(ctx, 1)
	s.isAnchor = true
	ctx.clear()
	return true
}

func (s *Scanner) scanAlias(ctx *Context) bool {
	if ctx.existsBuffer() {
		return false
	}

	s.addBufferedTokenIfExists(ctx)
	ctx.addOriginBuf('*')
	ctx.addToken(token.Alias(string(ctx.obuf), s.pos()))
	s.progressColumn(ctx, 1)
	s.isAlias = true
	ctx.clear()
	return true
}

// inName reports whether the scan stands in the name of an anchor or an
// alias, right after its indicator token. The anchor and alias flags stay
// set after an error and after a tag that follows the indicator. So the
// flags alone do not show that the scan stands in a name. niceyaml:
// upstream does not have this function.
func (s *Scanner) inName(ctx *Context) bool {
	if !s.isAnchor && !s.isAlias {
		return false
	}
	tk := ctx.lastToken()
	return tk != nil && (tk.Type == token.AnchorType || tk.Type == token.AliasType)
}

// blankRunEndsLine reports whether the run of spaces and tabs that starts
// at the rune index from ends at a line break. niceyaml: upstream does not
// have this function.
func blankRunEndsLine(ctx *Context, from int) bool {
	for i := from; i < ctx.size; i++ {
		switch ctx.src[i] {
		case ' ', '\t':
			continue
		case '\n', '\r':
			return true
		}
		return false
	}
	return false
}

// blankRunTabEnd returns the rune index past the last tab of the run of
// spaces and tabs that starts at the rune index from. It returns from when
// the run holds no tab. niceyaml: upstream does not have this function.
func blankRunTabEnd(ctx *Context, from int) int {
	end := from
	for i := from; i < ctx.size; i++ {
		c := ctx.src[i]
		if c != ' ' && c != '\t' {
			break
		}
		if c == '\t' {
			end = i + 1
		}
	}
	return end
}

// closeName ends the name of an anchor or an alias at the space or the tab
// where the scan stands. The name token takes the run of spaces and tabs
// from there through the last tab of the run, as a tag does. So the origin
// of the next token does not open with a tab. The spaces after the last
// tab open that origin as after any token. A key after the blanks then
// reads as a key after a space. niceyaml: upstream does not have this
// function.
func (s *Scanner) closeName(ctx *Context) {
	s.addBufferedTokenIfExists(ctx)
	s.isAnchor = false
	s.isAlias = false

	end := blankRunTabEnd(ctx, ctx.idx)
	tk := ctx.lastToken()
	tk.Origin += ctx.source(ctx.idx, end)
	s.progressColumn(ctx, end-ctx.idx)
}

func (s *Scanner) scanReservedChar(ctx *Context, c rune) error {
	if ctx.existsBuffer() {
		return nil
	}

	ctx.addBuf(c)
	ctx.addOriginBuf(c)
	err := ErrInvalidToken(
		token.Invalid(
			fmt.Sprintf("%q is a reserved character", c),
			string(ctx.obuf), s.pos(),
		),
	)
	s.progressColumn(ctx, 1)
	ctx.clear()
	return err
}

func (s *Scanner) scanTab(ctx *Context, c rune) error {
	if s.startedFlowSequenceNum > 0 || s.startedFlowMapNum > 0 {
		// tabs character is allowed in flow mode.
		return nil
	}

	if !s.isFirstCharAtLine {
		return nil
	}

	ctx.addBuf(c)
	ctx.addOriginBuf(c)
	err := ErrInvalidToken(
		token.Invalid("found character '\t' that cannot start any token",
			string(ctx.obuf), s.pos(),
		),
	)
	s.progressColumn(ctx, 1)
	ctx.clear()
	return err
}

func (s *Scanner) scan(ctx *Context) error {
	for ctx.next() {
		c := ctx.currentChar()
		// First, change the IndentState.
		// If the target character is the first character in a line, IndentState is Up/Down/Equal state.
		// The second and subsequent letters are Keep.
		s.updateIndent(ctx, c)

		// If IndentState is down, tokens are split, so the buffer accumulated until that point needs to be cutted as a token.
		if s.isChangedToIndentStateDown() {
			s.addBufferedTokenIfExists(ctx)
			// niceyaml: a line left of the tag closes the tag, so the tag
			// has no value.
			if tk := ctx.lastToken(); tk != nil && tk.Type == token.TagType {
				ctx.tagClosed = true
			}
		}
		if ctx.isMultiLine() {
			if s.isChangedToIndentStateDown() {
				if tk := ctx.lastToken(); tk != nil {
					// If literal/folded content is empty, no string token is added.
					// Therefore, add an empty string token.
					// But if literal/folded token column is 1, it is invalid at down state.
					if tk.Position.Column == 1 {
						return ErrInvalidToken(
							token.Invalid(
								"could not find multi-line content",
								string(ctx.obuf), s.pos(),
							),
						)
					}
					if tk.Type != token.StringType {
						ctx.addToken(token.String("", "", s.pos()))
					}
				}
				s.breakMultiLine(ctx)
			} else {
				if err := s.scanMultiLine(ctx, c); err != nil {
					return err
				}
				continue
			}
		}
		switch c {
		case '{':
			if s.scanFlowMapStart(ctx) {
				continue
			}
		case '}':
			if s.scanFlowMapEnd(ctx) {
				continue
			}
		case '.':
			if s.scanDocumentEnd(ctx) {
				continue
			}
		case '<':
			if s.scanMergeKey(ctx) {
				continue
			}
		case '-':
			if s.scanDocumentStart(ctx) {
				continue
			}
			if s.scanRawFoldedChar(ctx) {
				continue
			}
			scanned, err := s.scanSequence(ctx)
			if err != nil {
				return err
			}
			if scanned {
				continue
			}
		case '[':
			if s.scanFlowArrayStart(ctx) {
				continue
			}
		case ']':
			if s.scanFlowArrayEnd(ctx) {
				continue
			}
		case ',':
			if s.scanFlowEntry(ctx, c) {
				continue
			}
		case ':':
			scanned, err := s.scanMapDelim(ctx)
			if err != nil {
				return err
			}
			if scanned {
				continue
			}
		case '|', '>':
			scanned, err := s.scanMultiLineHeader(ctx)
			if err != nil {
				return err
			}
			if scanned {
				continue
			}
		case '!':
			scanned, err := s.scanTag(ctx)
			if err != nil {
				return err
			}
			if scanned {
				continue
			}
		case '%':
			if s.scanDirective(ctx) {
				continue
			}
		case '?':
			if s.scanMapKey(ctx) {
				continue
			}
		case '&':
			if s.scanAnchor(ctx) {
				continue
			}
		case '*':
			if s.scanAlias(ctx) {
				continue
			}
		case '#':
			if s.scanComment(ctx) {
				continue
			}
		case '\'', '"':
			scanned, err := s.scanQuote(ctx, c)
			if err != nil {
				return err
			}
			if scanned {
				continue
			}
		case '\r', '\n':
			s.scanNewLine(ctx, c)
			if s.heldLineEnd {
				// niceyaml: the token before holds this line ending.
				s.heldLineEnd = false
				ctx.obuf = ctx.obuf[:0]
				ctx.notSpaceOrgCharPos = 0
				ctx.lineBefore = true
			}
			continue
		case ' ':
			if s.scanWhiteSpace(ctx) {
				continue
			}
		case '@', '`':
			if err := s.scanReservedChar(ctx, c); err != nil {
				return err
			}
		case '\t':
			// niceyaml: a tab closes an anchor or an alias, as a space
			// does. Upstream reads the tab and the text after it into the
			// name. When the blanks run to a line break, upstream already
			// ends the name there and gives the name token the blanks and
			// the line break, as after a space.
			if s.inName(ctx) && !blankRunEndsLine(ctx, ctx.idx) {
				s.closeName(ctx)
				continue
			}
			if ctx.existsBuffer() && s.lastDelimColumn == 0 {
				// tab indent for plain text (yaml-test-suite's spec-example-7-12-plain-lines).
				s.indentNum++
				ctx.addOriginBuf(c)
				s.progressOnly(ctx, 1)
				continue
			}
			if s.lastDelimColumn < s.column {
				s.indentNum++
				ctx.addOriginBuf(c)
				s.progressOnly(ctx, 1)
				continue
			}
			if err := s.scanTab(ctx, c); err != nil {
				return err
			}
		}
		ctx.addBuf(c)
		ctx.addOriginBuf(c)
		s.progressColumn(ctx, 1)
	}
	s.addBufferedTokenIfExists(ctx)
	s.rest = string(ctx.obuf) // niceyaml: keep the text that ends the input.
	return nil
}

// Init prepares the scanner s to tokenize the text src by setting the scanner at the beginning of src.
func (s *Scanner) Init(text string) {
	src := []rune(text)
	s.source = src
	s.sourcePos = 0
	s.sourceSize = len(src)
	s.line = 1
	s.column = 1
	s.offset = 1
	s.isFirstCharAtLine = true
	s.headerLineEnd = false // niceyaml: no header of the new source is out yet.
	s.clearState()
}

// closeFlows forgets the flow collections that a document left open, so
// the next document does not scan as the inside of one. niceyaml: upstream
// keeps the counts across a "---" or "..." marker.
func (s *Scanner) closeFlows() {
	s.startedFlowSequenceNum = 0
	s.startedFlowMapNum = 0
}

func (s *Scanner) clearState() {
	s.prevLineIndentNum = 0
	s.lastDelimColumn = 0
	s.indentLevel = 0
	s.indentNum = 0
}

// Scan scans the next token and returns the token collection. The source end is indicated by io.EOF.
func (s *Scanner) Scan() (token.Tokens, error) {
	if s.sourcePos >= s.sourceSize {
		return nil, io.EOF
	}
	ctx := newContext(s.source[s.sourcePos:])
	// niceyaml: upstream opens this scan with the line ending of the
	// header before.
	ctx.lineBefore = s.headerLineEnd
	s.headerLineEnd = false
	defer ctx.release()

	var tokens token.Tokens
	err := s.scan(ctx)
	tokens = append(tokens, ctx.tokens...)

	if err != nil {
		var invalidTokenErr *InvalidTokenError
		if errors.As(err, &invalidTokenErr) {
			tokens = append(tokens, invalidTokenErr.Token)
		}
		return tokens, err
	}
	// niceyaml: the last token of the scan takes the text that ends the
	// input.
	if s.rest != "" && len(tokens) > 0 {
		tokens[len(tokens)-1].Origin += s.rest
		s.rest = ""
	}
	return tokens, nil
}
