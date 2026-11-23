package yamlscan

import (
	"errors"
	"strconv"
	"strings"
	"sync"

	"github.com/goccy/go-yaml/token"
)

// Context context at scanning
type Context struct {
	idx                int
	size               int
	notSpaceCharPos    int
	notSpaceOrgCharPos int
	src                []rune
	buf                []rune
	obuf               []rune
	tokens             token.Tokens
	mstate             *MultiLineState
	// niceyaml: tagClosed reports that a line at or left of the last
	// delimiter follows the tag that is the last token, so the tag has no
	// value and does not type the next token.
	tagClosed bool
	// niceyaml: tagEndedLine reports that a line break follows the tag
	// that is the last token. A key on a later line is then the first key
	// of the mapping that the tag is on, and the tag does not type it.
	tagEndedLine bool
	// niceyaml: compat holds the option of the scanner that
	// [WithCompat] sets.
	compat bool
	// niceyaml: lineBefore reports that the token before holds the line
	// ending in front of the origin buffer, where upstream opens the
	// buffer with that line ending.
	lineBefore bool
}

type MultiLineState struct {
	opt                              string
	firstLineIndentColumn            int
	prevLineIndentColumn             int
	lineIndentColumn                 int
	lastNotSpaceOnlyLineIndentColumn int
	spaceOnlyIndentColumn            int
	foldedNewLine                    bool
	isRawFolded                      bool
	isLiteral                        bool
	isFolded                         bool
}

var (
	ctxPool = sync.Pool{
		New: func() interface{} {
			return createContext()
		},
	}
)

func createContext() *Context {
	return &Context{
		idx:    0,
		tokens: token.Tokens{},
	}
}

func newContext(src []rune) *Context {
	ctx, _ := ctxPool.Get().(*Context)
	ctx.reset(src)
	return ctx
}

func (c *Context) release() {
	ctxPool.Put(c)
}

func (c *Context) clear() {
	c.resetBuffer()
	c.mstate = nil
	c.lineBefore = false // niceyaml: the next origin buffer starts empty.
}

func (c *Context) reset(src []rune) {
	c.idx = 0
	c.size = len(src)
	c.src = src
	c.tokens = c.tokens[:0]
	c.resetBuffer()
	c.mstate = nil
	// niceyaml: a new scan starts without a tag before it.
	c.tagClosed = false
	c.tagEndedLine = false
	c.lineBefore = false // niceyaml: and without a line ending before it.
}

func (c *Context) resetBuffer() {
	c.buf = c.buf[:0]
	c.obuf = c.obuf[:0]
	c.notSpaceCharPos = 0
	c.notSpaceOrgCharPos = 0
}

func (c *Context) breakMultiLine() {
	c.mstate = nil
}

func (c *Context) getMultiLineState() *MultiLineState {
	return c.mstate
}

func (c *Context) setLiteral(lastDelimColumn int, opt string) {
	mstate := &MultiLineState{
		isLiteral: true,
		opt:       opt,
	}
	indent := firstLineIndentColumnByOpt(opt)
	if indent > 0 {
		mstate.firstLineIndentColumn = lastDelimColumn + indent
	}
	c.mstate = mstate
}

func (c *Context) setFolded(lastDelimColumn int, opt string) {
	mstate := &MultiLineState{
		isFolded: true,
		opt:      opt,
	}
	indent := firstLineIndentColumnByOpt(opt)
	if indent > 0 {
		mstate.firstLineIndentColumn = lastDelimColumn + indent
	}
	c.mstate = mstate
}

func (c *Context) setRawFolded(column int) {
	mstate := &MultiLineState{
		isRawFolded: true,
	}
	mstate.updateIndentColumn(column)
	c.mstate = mstate
}

func firstLineIndentColumnByOpt(opt string) int {
	opt = strings.TrimPrefix(opt, "-")
	opt = strings.TrimPrefix(opt, "+")
	opt = strings.TrimSuffix(opt, "-")
	opt = strings.TrimSuffix(opt, "+")
	i, _ := strconv.ParseInt(opt, 10, 64)
	return int(i)
}

func (s *MultiLineState) lastDelimColumn() int {
	if s.firstLineIndentColumn == 0 {
		return 0
	}
	return s.firstLineIndentColumn - 1
}

func (s *MultiLineState) updateIndentColumn(column int) {
	if s.firstLineIndentColumn == 0 {
		s.firstLineIndentColumn = column
	}
	if s.lineIndentColumn == 0 {
		s.lineIndentColumn = column
	}
}

func (s *MultiLineState) updateSpaceOnlyIndentColumn(column int) {
	if s.firstLineIndentColumn != 0 {
		return
	}
	s.spaceOnlyIndentColumn = column
}

func (s *MultiLineState) validateIndentAfterSpaceOnly(column int) error {
	if s.firstLineIndentColumn != 0 {
		return nil
	}
	if s.spaceOnlyIndentColumn > column {
		return errors.New("invalid number of indent is specified after space only")
	}
	return nil
}

func (s *MultiLineState) validateIndentColumn() error {
	if firstLineIndentColumnByOpt(s.opt) == 0 {
		return nil
	}
	if s.firstLineIndentColumn > s.lineIndentColumn {
		return errors.New("invalid number of indent is specified in the multi-line header")
	}
	return nil
}

func (s *MultiLineState) updateNewLineState() {
	s.prevLineIndentColumn = s.lineIndentColumn
	if s.lineIndentColumn != 0 {
		s.lastNotSpaceOnlyLineIndentColumn = s.lineIndentColumn
	}
	s.foldedNewLine = true
	s.lineIndentColumn = 0
}

func (s *MultiLineState) isIndentColumn(column int) bool {
	if s.firstLineIndentColumn == 0 {
		return column == 1
	}
	return s.firstLineIndentColumn > column
}

func (s *MultiLineState) addIndent(ctx *Context, column int) {
	if s.firstLineIndentColumn == 0 {
		return
	}

	// If the first line of the document has already been evaluated, the number is treated as the threshold, since the `firstLineIndentColumn` is a positive number.
	if column < s.firstLineIndentColumn {
		return
	}

	// `c.foldedNewLine` is a variable that is set to true for every newline.
	if !s.isLiteral && s.foldedNewLine {
		s.foldedNewLine = false
	}
	// Since addBuf ignore space character, add to the buffer directly.
	ctx.buf = append(ctx.buf, ' ')
	ctx.notSpaceCharPos = len(ctx.buf)
}

// updateNewLineInFolded if Folded or RawFolded context and the content on the current line starts at the same column as the previous line,
// treat the new-line-char as a space.
func (s *MultiLineState) updateNewLineInFolded(ctx *Context, column int) {
	if s.isLiteral {
		return
	}

	// Folded or RawFolded.

	if !s.foldedNewLine {
		return
	}
	var (
		lastChar     rune
		prevLastChar rune
	)
	if len(ctx.buf) != 0 {
		lastChar = ctx.buf[len(ctx.buf)-1]
	}
	if len(ctx.buf) > 1 {
		prevLastChar = ctx.buf[len(ctx.buf)-2]
	}
	if s.lineIndentColumn == s.prevLineIndentColumn {
		// ---
		// >
		//  a
		//  b
		if lastChar == '\n' {
			ctx.buf[len(ctx.buf)-1] = ' '
		}
	} else if s.prevLineIndentColumn == 0 && s.lastNotSpaceOnlyLineIndentColumn == column {
		// if previous line is indent-space and new-line-char only, prevLineIndentColumn is zero.
		// In this case, last new-line-char is removed.
		// ---
		// >
		//  a
		//
		//  b
		if lastChar == '\n' && prevLastChar == '\n' {
			ctx.buf = ctx.buf[:len(ctx.buf)-1]
			ctx.notSpaceCharPos = len(ctx.buf)
		}
	}
	s.foldedNewLine = false
}

func (s *MultiLineState) hasTrimAllEndNewlineOpt() bool {
	return strings.HasPrefix(s.opt, "-") || strings.HasSuffix(s.opt, "-") || s.isRawFolded
}

func (s *MultiLineState) hasKeepAllEndNewlineOpt() bool {
	return strings.HasPrefix(s.opt, "+") || strings.HasSuffix(s.opt, "+")
}

func (c *Context) addToken(tk *token.Token) {
	if tk == nil {
		return
	}
	// niceyaml: the flags describe the tag that is the last token, so a
	// new last token clears them.
	c.tagClosed = false
	c.tagEndedLine = false
	c.tokens = append(c.tokens, tk)
}

func (c *Context) addBuf(r rune) {
	if len(c.buf) == 0 && (r == ' ' || r == '\t') {
		return
	}
	c.buf = append(c.buf, r)
	if r != ' ' && r != '\t' {
		c.notSpaceCharPos = len(c.buf)
	}
}

func (c *Context) addBufWithTab(r rune) {
	if len(c.buf) == 0 && r == ' ' {
		return
	}
	c.buf = append(c.buf, r)
	if r != ' ' {
		c.notSpaceCharPos = len(c.buf)
	}
}

func (c *Context) addOriginBuf(r rune) {
	c.obuf = append(c.obuf, r)
	if r != ' ' && r != '\t' {
		c.notSpaceOrgCharPos = len(c.obuf)
	}
}

// abandoned returns tk, the invalid token of an escape that the scanner
// abandons, with an Origin that lacks the backslash that ends it. The
// scanner reads the escape again from that backslash, so the next token
// holds it. With compat, a tab earlier in the scalar moves the next scan
// past the backslash, and [Scanner.Scan] gives it back to tk. The Value
// keeps the backslash, as upstream writes it.
// niceyaml: upstream does not have this function.
func abandoned(tk *token.Token) *token.Token {
	tk.Origin = strings.TrimSuffix(tk.Origin, `\`)
	return tk
}

// addOriginRunes adds rs to the origin buffer. niceyaml: upstream does not
// have this function.
func (c *Context) addOriginRunes(rs []rune) {
	for _, r := range rs {
		c.addOriginBuf(r)
	}
}

// removeRightSpaceFromBuf reports whether the origin buffer ends with
// spaces or tabs. niceyaml: the origin keeps them, where upstream cuts
// them off.
func (c *Context) removeRightSpaceFromBuf() bool {
	trimmedBuf := c.obuf[:c.notSpaceOrgCharPos]
	buflen := len(trimmedBuf)
	diff := len(c.obuf) - buflen
	if diff > 0 {
		c.buf = c.bufferedSrc()
	}
	return diff > 0
}

// tabIndented reports whether the origin buffer opens with a tab, after
// one space at most, as upstream reads it from a buffer without the
// spaces and tabs that end a line. A first line of spaces and tabs alone
// thus does not count. With compat, a buffer after a line ending that the
// token before holds does not count either, because upstream opens that
// buffer with the line ending. niceyaml: upstream does not have this
// function.
func (c *Context) tabIndented() bool {
	if c.compat && c.lineBefore {
		return false
	}
	i := 0
	if len(c.obuf) > 0 && c.obuf[0] == ' ' {
		i = 1
	}
	if i >= len(c.obuf) || c.obuf[i] != '\t' {
		return false
	}
	for _, r := range c.obuf {
		if r != ' ' && r != '\t' {
			return r != '\n' && r != '\r'
		}
	}
	return true
}

func (c *Context) isEOS() bool {
	return len(c.src)-1 <= c.idx
}

func (c *Context) isNextEOS() bool {
	return len(c.src) <= c.idx+1
}

func (c *Context) next() bool {
	return c.idx < c.size
}

func (c *Context) source(s, e int) string {
	return string(c.src[s:e])
}

func (c *Context) previousChar() rune {
	if c.idx > 0 {
		return c.src[c.idx-1]
	}
	return rune(0)
}

func (c *Context) currentChar() rune {
	if c.size > c.idx {
		return c.src[c.idx]
	}
	return rune(0)
}

func (c *Context) nextChar() rune {
	if c.size > c.idx+1 {
		return c.src[c.idx+1]
	}
	return rune(0)
}

func (c *Context) repeatNum(r rune) int {
	cnt := 0
	for i := c.idx; i < c.size; i++ {
		if c.src[i] == r {
			cnt++
		} else {
			break
		}
	}
	return cnt
}

func (c *Context) progress(num int) {
	c.idx += num
}

func (c *Context) existsBuffer() bool {
	return len(c.bufferedSrc()) != 0
}

func (c *Context) isMultiLine() bool {
	return c.mstate != nil
}

func (c *Context) bufferedSrc() []rune {
	src := c.buf[:c.notSpaceCharPos]
	if c.isMultiLine() {
		mstate := c.getMultiLineState()
		// remove end '\n' character and trailing empty lines.
		// https://yaml.org/spec/1.2.2/#8112-block-chomping-indicator
		if mstate.hasTrimAllEndNewlineOpt() {
			// If the '-' flag is specified, all trailing newline characters will be removed.
			src = []rune(strings.TrimRight(string(src), "\n"))
		} else if !mstate.hasKeepAllEndNewlineOpt() {
			// Normally, all but one of the trailing newline characters are removed.
			var newLineCharCount int
			for i := len(src) - 1; i >= 0; i-- {
				if src[i] == '\n' {
					newLineCharCount++
					continue
				}
				break
			}
			removedNewLineCharCount := newLineCharCount - 1
			for removedNewLineCharCount > 0 {
				src = []rune(strings.TrimSuffix(string(src), "\n"))
				removedNewLineCharCount--
			}
		}

		// If the text ends with a space character, remove all of them.
		if mstate.hasTrimAllEndNewlineOpt() {
			src = []rune(strings.TrimRight(string(src), " "))
		}
		if string(src) == "\n" {
			// If the content consists only of a newline,
			// it can be considered as the document ending without any specified value,
			// so it is treated as an empty string.
			src = []rune{}
		}
		if mstate.hasKeepAllEndNewlineOpt() && len(src) == 0 {
			src = []rune{'\n'}
		}
	}
	return src
}

func (c *Context) bufferedToken(pos *token.Position) *token.Token {
	if c.idx == 0 {
		return nil
	}
	source := c.bufferedSrc()
	if len(source) == 0 {
		c.buf = c.buf[:0] // clear value's buffer only.
		return nil
	}
	var tk *token.Token
	if c.isMultiLine() {
		tk = token.String(string(source), string(c.obuf), pos)
	} else {
		tk = token.New(string(source), string(c.obuf), pos)
	}
	c.setTokenTypeByPrevTag(tk)
	c.resetBuffer()
	return tk
}

func (c *Context) setTokenTypeByPrevTag(tk *token.Token) {
	lastTk := c.lastToken()
	if lastTk == nil {
		return
	}
	// niceyaml: without compat, a tag that a later line closes does not
	// type this token.
	if lastTk.Type != token.TagType || c.tagClosed && !c.compat {
		return
	}
	tag := token.ReservedTagKeyword(lastTk.Value)
	if _, exists := token.ReservedTagKeywordMap[tag]; !exists {
		tk.Type = token.StringType
	}
}

func (c *Context) lastToken() *token.Token {
	if len(c.tokens) != 0 {
		return c.tokens[len(c.tokens)-1]
	}
	return nil
}
