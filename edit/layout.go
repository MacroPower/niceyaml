package edit

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
)

// defaultStep is the indent step of a document that does not hold a nested
// mapping.
const defaultStep = 2

// region is the lines of an entry or an item of a block collection, with
// the comments that belong to it.
type region struct {
	// The bytes of the lines, each with its line break.
	start int
	end   int
	// The first and the last line.
	first int
	last  int
}

// blockRegion returns the lines of p, an entry or an item of a block
// collection, with its comments. The comment lines directly above it that
// start in its column belong to it, unless they reach the top of the
// document. When p shares its first line with the "-" of an item, no
// comment line above belongs to it. Each comment line after its value that
// starts deeper than its key before any other content belongs to it too.
func (d *Doc) blockRegion(p *place) region {
	start, _ := d.startOf(p)
	keyLine := d.lineOf(start)
	keyCol := d.column(start)

	first := keyLine

	for l := keyLine - 1; l >= 0 && !d.sharesLine(p); l-- {
		col, ok := d.commentColumn(l)
		if !ok || col != keyCol {
			break
		}

		first = l
	}

	if first < keyLine && d.topComments(first) {
		first = keyLine
	}

	end := d.valueEnd(p)
	last := d.lineOf(max(start, end-1))
	last = max(last, keyLine)

	if d.keepsTrailingLines(p) {
		last = d.keptLinesAfter(last)
	}

	for l := last + 1; l < len(d.curLines) && d.lineStart(l) < len(d.cur); l++ {
		if d.isBlankLine(l) {
			continue
		}

		col, ok := d.commentColumn(l)
		if !ok || col <= keyCol {
			break
		}

		last = l
	}

	return region{start: d.lineStart(first), end: d.lineStart(last + 1), first: first, last: last}
}

// sharesLine reports whether p, an entry or an item of a block collection,
// starts on the line of the "-" of the item that holds the collection. The
// first entry of a mapping item does.
func (d *Doc) sharesLine(p *place) bool {
	start, ok := d.startOf(p)

	return ok && d.column(start) > d.indentOf(d.lineOf(start))
}

// topComments reports whether the lines above line hold only comments and
// blank lines up to the top of the document. Comment lines that reach the
// top belong to the document and stay at its top.
func (d *Doc) topComments(line int) bool {
	for l := line - 1; l >= 0; l-- {
		text := strings.TrimSpace(d.lineText(l))

		switch {
		case text == "" || strings.HasPrefix(text, "#"):
			continue
		case isHeader(text):
			return true
		default:
			return false
		}
	}

	return true
}

// keepsTrailingLines reports whether the text of the value of p ends with
// a block scalar with keep chomping, at any depth. The string of that
// scalar holds the blank lines after its text, so they belong to the value.
func (d *Doc) keepsTrailingLines(p *place) bool {
	return endsKeeping(p.value)
}

// endsKeeping reports whether the text of node ends with a block scalar
// with keep chomping, whose blank lines go with node.
func endsKeeping(node ast.Node) bool {
	lit := endScalar(node)

	return lit != nil && keepsChomping(lit)
}

// keptLinesAfter returns the last of the blank lines right after line, or
// line when none follows it. A block scalar with keep chomping that ends
// on line holds them.
func (d *Doc) keptLinesAfter(line int) int {
	for line+1 < len(d.curLines) && d.isBlankLine(line+1) && d.lineStart(line+1) < len(d.cur) {
		line++
	}

	return line
}

// endScalar returns the block scalar that the text of node ends with: node
// itself, or the last value of a block collection that ends with one, at
// any depth. It returns nil for a text that ends otherwise.
func endScalar(node ast.Node) *ast.LiteralNode {
	for !astnode.IsNil(node) {
		switch x := astnode.Content(node).(type) {
		case *ast.LiteralNode:
			return x

		case *ast.MappingNode:
			if x.IsFlowStyle || len(x.Values) == 0 {
				return nil
			}

			node = x.Values[len(x.Values)-1].Value

		case *ast.MappingValueNode:
			node = x.Value

		case *ast.SequenceNode:
			if x.IsFlowStyle || len(x.Values) == 0 {
				return nil
			}

			node = x.Values[len(x.Values)-1]

		default:
			return nil
		}
	}

	return nil
}

// keepsBlanks reports whether the lines of node, which end with the line
// last, end with a block scalar with keep chomping, so that its string
// holds the blank lines after its text. A comment line after the scalar
// ends the lines otherwise.
func (d *Doc) keepsBlanks(node ast.Node, last int) bool {
	lit := endScalar(node)
	if lit == nil || !keepsChomping(lit) {
		return false
	}

	s, ok := d.nodeSpan(lit)
	if !ok {
		return false
	}

	for l := d.lineOf(max(s.start, s.end-1)) + 1; l <= last; l++ {
		if !d.isBlankLine(l) {
			return false
		}
	}

	return true
}

// tailScalar is the block scalar whose text ends the lines of a value, as
// [Doc.scalarTail] reads it.
type tailScalar struct {
	// The number of the last lines of the value that hold the text of the
	// scalar, or 0 when the lines end otherwise.
	lines int
	// The number of spaces that the least indented of those lines holds
	// past the indent of the scalar. An indentation indicator can give the
	// scalar an indent less than that of its lines.
	extra int
	// The digit of the indentation indicator, or 0 without one, and the
	// origin of the first byte of the header of the scalar.
	digit, origin int
}

// scalarTail returns the block scalar that the text of node ends with,
// whose text runs through the line last at most. The lines after its
// indicator through last hold its text, unless a line after that text
// through last is not blank, such as a comment line of the value. The
// count of lines is 0 then.
func (d *Doc) scalarTail(node ast.Node, last int) tailScalar {
	lit := endScalar(node)
	if lit == nil || lit.Start == nil {
		return tailScalar{}
	}

	hs, ok := d.tokenSpan(lit.Start)
	if !ok {
		return tailScalar{}
	}

	s, ok := d.nodeSpan(lit)
	if !ok {
		return tailScalar{}
	}

	header := d.lineOf(hs.start)
	t := tailScalar{lines: max(0, last-header)}

	for l := d.lineOf(max(s.start, s.end-1)) + 1; l <= last; l++ {
		if !d.isBlankLine(l) {
			return tailScalar{}
		}
	}

	if digit, i := indentDigit(lit.Start.Value); digit > 0 {
		if at := hs.start + i; at >= len(d.cur) || d.cur[at] != lit.Start.Value[i] {
			return tailScalar{}
		}

		t.digit, t.origin = digit, d.origin[hs.start]

		indent := d.scalarIndent(lit, last)
		least := -1

		for l := header + 1; l <= last; l++ {
			if !d.isBlankLine(l) && (least < 0 || d.indentOf(l) < least) {
				least = d.indentOf(l)
			}
		}

		if indent < 0 || least < indent {
			return tailScalar{}
		}

		t.extra = least - indent
	}

	return t
}

// indentPast returns text, lines that an edit writes, whose last lines
// hold the text of t, a block scalar. A block scalar reads each line after
// it that indents as deep as the scalar does. The first line from line on
// that is not blank can do so, such as a comment line that the edit keeps.
// The lines of the scalar then go one space past that line, and its
// indentation indicator grows by as many spaces. An indicator stays as it
// is past 9, the largest that the parser takes. The lines of text can end
// with any line break, as the lines of a scalar that keep their own do.
func (d *Doc) indentPast(text run, t tailScalar, line int) run {
	next := -1

	for l := line; t.lines > 0 && l < len(d.curLines) && d.lineStart(l) < len(d.cur); l++ {
		if !d.isBlankLine(l) {
			next = d.indentOf(l)

			break
		}
	}

	body := text.text
	starts := lineStarts(body)

	if next < 0 || len(starts) <= t.lines {
		return text
	}

	header := starts[len(starts)-t.lines-1]
	starts = starts[len(starts)-t.lines:]
	indent := -1

	for i, s := range starts {
		end := len(body)
		if i+1 < len(starts) {
			end = starts[i+1]
		}

		l := strings.TrimSuffix(strings.TrimSuffix(body[s:end], "\n"), "\r")
		trimmed := strings.TrimLeft(l, " ")
		if strings.TrimSpace(l) != "" && (indent < 0 || len(l)-len(trimmed) < indent) {
			indent = len(l) - len(trimmed)
		}
	}

	if indent < 0 || indent-t.extra > next {
		return text
	}

	shift := next + 1 - (indent - t.extra)
	out := run{text: body, origins: text.origins}

	if t.digit > 0 {
		at := indicatorAt(text, header, starts[0], t)
		if at < 0 || t.digit+shift > 9 {
			return text
		}

		out.text = body[:at] + strconv.Itoa(t.digit+shift) + body[at+1:]
	}

	pad := strings.Repeat(" ", shift)
	shifted := run{}
	prev := 0

	for _, s := range starts {
		if s == len(body) || body[s] == '\n' || body[s] == '\r' {
			continue
		}

		shifted = appendRun(shifted, run{text: out.text[prev:s], origins: out.origins[prev:s]})
		shifted = appendRun(shifted, newRun(pad, out.origins[s]))
		prev = s
	}

	return appendRun(shifted, run{text: out.text[prev:], origins: out.origins[prev:]})
}

// indicatorAt returns the offset in text of the digit of the indentation
// indicator of t, on the line of text from start to end, or -1. The header
// of the scalar starts at the first byte there with the origin that t
// gives.
func indicatorAt(text run, start, end int, t tailScalar) int {
	i := slices.Index(text.origins[start:end], t.origin) + start
	if digit, at := indentDigit(text.text[max(i, start):end]); i >= start && digit == t.digit {
		return i + at
	}

	return -1
}

// indentDigit returns the digit of the indentation indicator of header, the
// text of the header of a block scalar, and its offset in header, or 0 and
// -1 without one. The digit is the first one from 1 to 9 in header.
func indentDigit(header string) (int, int) {
	i := strings.IndexAny(header, "123456789")
	if i < 0 {
		return 0, -1
	}

	return int(header[i] - '0'), i
}

// blanksEnd returns the start of the first line from line on that is not
// blank, or the end of the text when only blank lines follow.
func (d *Doc) blanksEnd(line int) int {
	for line < len(d.curLines) && d.lineStart(line) < len(d.cur) && d.isBlankLine(line) {
		line++
	}

	return d.lineStart(line)
}

// scalarAbove returns the block scalar whose text ends right above line,
// with only blank lines after it, or nil. It also returns the last line of
// the text of the scalar, which can be a line of spaces alone, as
// [Doc.scalarEnd] tells.
func (d *Doc) scalarAbove(line int) (*ast.LiteralNode, int) {
	above := line - 1
	for above >= 0 && d.isBlankLine(above) {
		above--
	}

	if above < 0 {
		return nil, -1
	}

	for _, lit := range d.literals() {
		if s, ok := d.nodeSpan(lit); ok {
			if e := d.lineOf(max(s.start, s.end-1)); e >= above && e < line {
				return lit, e
			}
		}
	}

	return nil, 0
}

// literals returns the block scalars of the current text, in the order of
// the text. It reads them on its first call for a parse. The caller must
// not change the returned slice.
func (d *Doc) literals() []*ast.LiteralNode {
	t := d.parseIndex()
	if t.literals == nil {
		t.literals = append([]*ast.LiteralNode{}, d.walked().kinds().literals...)
	}

	return t.literals
}

// headerAt is a block scalar of the current text with a header, and the
// offset of its header.
type headerAt struct {
	lit   *ast.LiteralNode
	start int
}

// headers returns the block scalars of the current text with a header, by
// the offsets of their headers. It reads them on its first call for a
// parse. The caller must not change the returned slice.
func (d *Doc) headers() []headerAt {
	t := d.parseIndex()
	if t.headers == nil {
		t.headers = []headerAt{}

		for _, lit := range d.literals() {
			if lit.Start == nil {
				continue
			}

			if hs, ok := d.tokenSpan(lit.Start); ok {
				t.headers = append(t.headers, headerAt{lit: lit, start: hs.start})
			}
		}

		slices.SortStableFunc(t.headers, func(a, b headerAt) int { return cmp.Compare(a.start, b.start) })
	}

	return t.headers
}

// headersAfter returns the block scalars of the current text whose headers
// start after the offset after, as [Doc.headers] gives them.
func (d *Doc) headersAfter(after int) []headerAt {
	all := d.headers()
	i, _ := slices.BinarySearchFunc(all, after+1, func(h headerAt, off int) int { return cmp.Compare(h.start, off) })

	return all[i:]
}

// blanksAfter returns the end of the last line of the blank lines right
// after line, before its line break, or the end of line when no blank line
// follows it.
func (d *Doc) blanksAfter(line int) int {
	if blanks := d.blanksEnd(line + 1); blanks > d.lineStart(line+1) {
		return d.lineEnd(d.lineOf(blanks - 1))
	}

	return d.lineEnd(line)
}

// readUserScalars reads the string of each block scalar of the user's
// text into d.scalars, by the offset in orig of its indicator. [New] calls
// it while the current text is the text of the user.
func (d *Doc) readUserScalars() {
	d.scalars = map[int]string{}

	d.inspect(func(n ast.Node) bool {
		lit, ok := n.(*ast.LiteralNode)
		if !ok || lit.Start == nil || lit.Value == nil {
			return true
		}

		if hs, ok := d.tokenSpan(lit.Start); ok {
			d.scalars[hs.start] = lit.Value.Value
		}

		return true
	})
}

// heldString is a block scalar of the current text that an edit wrote,
// with the string that the edit gave it.
type heldString struct {
	lit *ast.LiteralNode
	// The header that the edit wrote, when the header is one that
	// [Doc.keepStrings] wrote in its place, or nil.
	first *run
	str   string
	// The bytes of the header, such as "|" or ">2-".
	header span
}

// stringAt returns the block scalar lit with the string that it keeps, as
// [heldString] describes, for a scalar that an edit wrote. The first byte
// of the header of such a scalar has a note that holds the string. It
// reports false for any other block scalar. A call that changes the string
// of a block scalar of the user's text fails, as [Doc.scalarProblem] tells.
func (d *Doc) stringAt(lit *ast.LiteralNode) (heldString, bool) {
	if lit == nil || lit.Start == nil || lit.Value == nil {
		return heldString{}, false
	}

	hs, ok := d.tokenSpan(lit.Start)
	if !ok || hs.start >= len(d.origin) {
		return heldString{}, false
	}

	o := d.origin[hs.start]
	if o >= 0 || !d.notes[-1-o].hasStr {
		return heldString{}, false
	}

	n := d.notes[-1-o]

	return heldString{lit: lit, header: hs, str: n.str, first: n.header}, true
}

// takesIn reports whether the parse reads text into x that is not its own:
// a byte of the user's text other than a blank on a line after the header,
// such as a comment line that the user indented under the value.
func (d *Doc) takesIn(x heldString) bool {
	s, ok := d.nodeSpan(x.lit)
	if !ok {
		return false
	}

	for i := d.lineEnd(d.lineOf(x.header.start)); i < s.end; i++ {
		if d.origin[i] >= 0 && !strings.ContainsRune(" \t\r\n", rune(d.cur[i])) {
			return true
		}
	}

	return false
}

// keepStrings writes again each block scalar of the current text that an
// edit wrote, whose string the edits of the call that h ran changed, or
// that the parse now reads past its own text. The string of a block scalar
// depends on the lines around its text. A line that an edit adds after the
// scalar can change what the parse reads from it, and so can a move of the
// scalar. Each such scalar keeps the string that its writer gave it, as
// [heldString] describes, and each keeps its own text. A scalar that
// keepStrings wrote again takes its writer's header back when that header
// keeps the string. It returns the problem of a scalar that no text keeps.
// A block scalar of the user's text takes back only the line breaks after
// its text, as [Doc.restoreBreaks] writes them.
func (d *Doc) keepStrings(ctx context.Context, h *Node) error {
	d.restoreTexts()

	after := -1

	for {
		x, ok := d.nextHeld(after)
		if !ok {
			break
		}

		after = x.header.start

		err := d.keepString(ctx, h, x)
		if err != nil {
			return err
		}
	}

	return d.restoreBreaks(h)
}

// restoreBreaks writes back the line breaks that the edits of a call took
// from the end of a block scalar of the user's text. A removal of the
// last entry of a text without a final line break takes the line break
// before the entry, and a move to the end of such a text writes its value
// without one. A removal can also take the blank lines that a scalar with
// keep chomping holds. The string of the scalar lacks its last line breaks
// then. At the end of the text, go-yaml can also fold the lines of a
// folded scalar in another way without its line break. Each such scalar
// takes as many line breaks after its text as its string lacks, or one at
// the end of the text, when that keeps its string. Its header and its lines
// keep their bytes. A scalar whose string changes in any other way keeps
// the text that the edits gave it, as [Doc.scalarProblem] tells. Go-yaml's
// parser refuses some texts with such line breaks, such as a text that
// ends with an empty line after a scalar with an indentation indicator, as
// in "|+2". The call of h then fails with [ErrEngine], which names the
// parser.
func (d *Doc) restoreBreaks(h *Node) error {
	after := -1

	for {
		lit, str, ok := d.nextUserScalar(after)
		if !ok {
			return nil
		}

		hs, _ := d.tokenSpan(lit.Start)
		after = hs.start

		s, ok := d.nodeSpan(lit)
		lack := trailingBreaks(str) - trailingBreaks(lit.Value.Value)
		lacks := lack > 0 && strings.TrimRight(str, "\n") == strings.TrimRight(lit.Value.Value, "\n")

		if !ok || lit.Value.Value == str || !lacks && strings.TrimSpace(d.cur[s.end:]) != "" {
			continue
		}

		saved := d.save()
		text := strings.Repeat(d.breakBefore(d.lineOf(s.end)+1), max(lack, 1))
		origin := d.addNote(d.keptNote(s.end, s.end))

		err := d.apply(edit{start: s.end, end: s.end, text: text, origins: written(text, origin)})
		if err != nil {
			return d.bug(h, err)
		}

		if again := d.literalAt(hs.start); again != nil && again.Value != nil && again.Value.Value == str {
			continue
		}

		d.restore(saved)
	}
}

// nextUserScalar returns the first block scalar of the current text whose
// header is a byte of the user's text that starts after the offset after,
// with the string that the user's text gives it, as
// [Doc.readUserScalars] reads it.
func (d *Doc) nextUserScalar(after int) (*ast.LiteralNode, string, bool) {
	for _, h := range d.headersAfter(after) {
		if h.lit.Value == nil || !d.ownByte(h.start) {
			continue
		}

		if str, ok := d.scalars[d.origin[h.start]]; ok {
			return h.lit, str, true
		}
	}

	return nil, "", false
}

// nextHeld returns the first block scalar of the current text whose header
// starts after the offset after, as [Doc.stringAt] reads it.
func (d *Doc) nextHeld(after int) (heldString, bool) {
	for _, h := range d.headersAfter(after) {
		if x, held := d.stringAt(h.lit); held {
			return x, true
		}
	}

	return heldString{}, false
}

// keepString keeps the string of x and its own text, as [Doc.keepStrings]
// describes. The header of the scalar can change: its style, its chomping
// indicator, and its indentation indicator, in the order of
// [headerForms]. The scalar can also take back line breaks that the
// parse reads into its string, as [Doc.tryHeader] tells. When no header
// keeps the string, the scalar takes the text that [Node.Set] writes for
// its string at its place, as [Doc.writeString] writes it.
func (d *Doc) keepString(ctx context.Context, h *Node, x heldString) error {
	fine := x.lit.Value.Value == x.str && !d.takesIn(x)
	if fine && x.first == nil {
		return nil
	}

	first := d.keptRun(x.header.start, x.header.end)
	if x.first != nil {
		first = *x.first
	}

	end := x.header.end
	if s, ok := d.nodeSpan(x.lit); ok {
		end = s.end
	}

	n := d.keptNote(x.header.start, end)
	n.str, n.hasStr, n.header = x.str, true, &first
	origin := d.addNote(n)

	if x.first != nil && d.tryHeader(x, *x.first, origin) {
		return nil
	}

	if fine {
		return nil
	}

	for _, form := range headerForms(d.cur[x.header.start:x.header.end]) {
		if d.tryHeader(x, newRun(form, origin), origin) {
			return nil
		}
	}

	return d.writeString(ctx, h, x, origin)
}

// tryHeader writes header in place of the header of x, and keeps the text
// when the parse then reads the string of x from its own text, as
// [Doc.takesIn] tells. An edit can take away a line break that the parse
// reads into the string of x. Such a line break can end the text, or a
// blank line that a scalar with keep chomping holds. So tryHeader also
// tries the text with line breaks after the last line of x, which take
// origin. It tries one line break, then the count that the string of x
// lacks with header alone, then one more than that count. The parse can
// read the last line of a scalar in another way without its line break,
// which the last count covers. It reports whether it kept a text, and
// leaves the text as it was otherwise.
func (d *Doc) tryHeader(x heldString, header run, origin int) bool {
	saved := d.save()
	need := trailingBreaks(x.str)
	tried := map[int]bool{}

	for _, k := range []int{0, 1, -1, -2} {
		if k < 0 {
			k = max(0, need) - k - 1
		}

		if tried[k] {
			continue
		}

		tried[k] = true
		edits := []edit{{start: x.header.start, end: x.header.end, text: header.text, origins: header.origins}}

		if k > 0 {
			s, ok := d.nodeSpan(x.lit)
			if !ok {
				break
			}

			text := strings.Repeat(d.breakBefore(d.lineOf(s.end)+1), k)
			edits = append(edits, edit{start: s.end, end: s.end, text: text, origins: written(text, origin)})
		}

		if d.apply(edits...) == nil {
			y, ok := d.stringAt(d.literalAt(x.header.start))
			if ok && y.lit.Value.Value == x.str && !d.takesIn(y) {
				return true
			}

			if ok && k == 0 {
				need = trailingBreaks(x.str) - trailingBreaks(y.lit.Value.Value)
			}
		}

		d.restore(saved)
	}

	return false
}

// trailingBreaks returns the number of line breaks that str ends with.
func trailingBreaks(str string) int {
	return len(str) - len(strings.TrimRight(str, "\n"))
}

// literalAt returns the block scalar of the current text whose header
// starts at off, or nil.
func (d *Doc) literalAt(off int) *ast.LiteralNode {
	if h := d.headersAfter(off - 1); len(h) > 0 && h[0].start == off {
		return h[0].lit
	}

	return nil
}

// headerForms returns the headers that a block scalar with the header
// header can take in its place, in the order that [Doc.keepString] tries
// them. Header itself comes first, which can keep the string with line
// breaks after the text of the scalar. The forms that change its chomping
// indicator come next, then the forms that change its indentation
// indicator too, so the first forms change the least. The forms of the
// other style come last. Each form writes the two indicators in the order
// of header, as in "|+2", or with the digit first when header lacks one of
// them.
func headerForms(header string) []string {
	style, digit, chomp := header[:1], "", ""
	chompFirst := false

	for _, c := range header[1:] {
		switch {
		case c >= '1' && c <= '9':
			digit = string(c)
		case c == '-' || c == '+':
			chomp = string(c)
			chompFirst = digit == ""
		}
	}

	chompFirst = chompFirst && digit != ""

	other := ">"
	if style == ">" {
		other = "|"
	}

	digits := []string{digit, "", "1", "2", "3", "4", "5", "6", "7", "8", "9"}
	chomps := []string{chomp, "", "-", "+"}

	forms := []string{header}
	seen := map[string]bool{header: true}

	for _, st := range []string{style, other} {
		for _, dg := range digits {
			for _, ch := range chomps {
				form := st + dg + ch
				if chompFirst {
					form = st + ch + dg
				}

				if !seen[form] {
					seen[form] = true
					forms = append(forms, form)
				}
			}
		}
	}

	return forms
}

// writeString writes the string of x as [Node.Set] writes it at the place
// of the scalar, for a scalar that no header keeps. The text that the parse
// reads into the scalar past its own text stays after the new text, such
// as a comment line of the user. The anchor and the tag of the scalar stay,
// and the new text takes origin, which binds where the scalar binds. The
// call of h fails with [ErrEngine] when the new text does not keep the
// string either, and for a scalar that is not the value of an entry, an
// item, or the root, such as a key.
func (d *Doc) writeString(ctx context.Context, h *Node, x heldString, origin int) error {
	var at *place

	switch p, ok := d.holderOf(x.lit); {
	case ok:
		at = p
	case astnode.Content(d.node.AST()) == x.lit:
		at = &place{root: true}
	default:
		return d.bug(h, fmt.Errorf("no text keeps the string %q of the block scalar", cut(x.str)))
	}

	at.value, at.end = x.lit, d.ownEnd(x)

	step := d.docStep(nil)
	if path, ok := d.node.PathAt(positionIn(d.cur, d.curLines, x.header.start)); ok {
		step = d.stepOf(nil, path)
	}

	// The text keeps the text that it replaces, which a later call can
	// write back, as [Doc.restoreTexts] does.
	replaced := &run{}
	d.notes[-1-origin].text = replaced

	pl, err := d.renderPayload(ctx, h, x.str, step, origin, nil)
	if err != nil {
		return err
	}

	edits := d.valueEdits(at, pl, origin, step)
	if len(edits) == 1 {
		*replaced = d.keptRun(edits[0].start, edits[0].end)
	}

	err = d.apply(edits...)
	if err != nil {
		return d.bug(h, err)
	}

	if y, ok := d.stringAt(d.literalAt(x.header.start)); ok && (y.lit.Value.Value != x.str || d.takesIn(y)) {
		return d.bug(h, fmt.Errorf("no text keeps the string %q of the block scalar", cut(x.str)))
	}

	return nil
}

// restoreTexts writes back the text of each block scalar that
// [Doc.writeString] replaced, when that text keeps its string at its place
// again, as [Doc.keepStrings] describes. The text that writeString wrote
// stands in one piece, and each of its bytes holds the replaced text.
func (d *Doc) restoreTexts() {
	for i := 0; i < len(d.cur); i++ {
		text := d.writtenText(i)
		if text == nil || text.text == "" {
			continue
		}

		end := i
		for end < len(d.cur) && d.writtenText(end) == text {
			end++
		}

		saved := d.save()

		if d.apply(edit{start: i, end: end, text: text.text, origins: text.origins}) == nil {
			if y, ok := d.stringAt(d.literalAt(i)); ok && y.lit.Value.Value == y.str && !d.takesIn(y) {
				i += len(text.text) - 1

				continue
			}
		}

		d.restore(saved)

		i = end - 1
	}
}

// writtenText returns the text that the byte at off stands in for, as a
// byte that [Doc.writeString] wrote, or nil.
func (d *Doc) writtenText(off int) *run {
	if o := d.origin[off]; o < 0 {
		return d.notes[-1-o].text
	}

	return nil
}

// ownEnd returns the end of the own text of x: the end of the last line
// after its header that is not blank and whose bytes are its own, as
// [Doc.takesIn] tells, or the end of its header.
func (d *Doc) ownEnd(x heldString) int {
	s, ok := d.nodeSpan(x.lit)
	if !ok {
		return x.header.end
	}

	end := x.header.end

	for l := d.lineOf(x.header.start) + 1; l < len(d.curLines) && d.lineStart(l) < s.end; l++ {
		start, stop := d.lineStart(l), min(d.lineEnd(l), s.end)

		for i := start; i < stop; i++ {
			if d.origin[i] >= 0 && !strings.ContainsRune(" \t", rune(d.cur[i])) {
				return end
			}
		}

		if !d.isBlankLine(l) {
			end = stop
		}
	}

	return end
}

// keepsChomping reports whether node holds a block scalar with keep
// chomping, whose string holds the blank lines after its text.
func keepsChomping(node ast.Node) bool {
	if astnode.IsNil(node) {
		return false
	}

	tk := astnode.Content(node).GetToken()

	return tk != nil && strings.Contains(tk.Value, "+") &&
		(strings.HasPrefix(tk.Value, "|") || strings.HasPrefix(tk.Value, ">"))
}

// stepOf returns the indent step of the block mapping at path, as from, a
// place of the text or nil, reads the path with [Doc.resolveFrom]. The
// step is the distance from the key of the entry that holds the mapping to
// its keys. For a mapping that is an item, it takes the step of the
// nearest mapping above it that is the value of an entry. At the root, it
// takes the step of the document.
func (d *Doc) stepOf(from *place, path paths.Path) int {
	for cur := path; !cur.IsRoot(); {
		last, _ := cur.Last()
		parent, _ := cur.Parent()

		if last.Kind == paths.SelectorChild {
			if step, ok := d.entryStep(from, cur); ok {
				return step
			}
		}

		cur = parent
	}

	return d.docStep(nil)
}

// entryStep returns the distance from the key of the entry at path, as
// from reads it, to the keys of the block mapping it holds.
func (d *Doc) entryStep(from *place, path paths.Path) (int, bool) {
	p, err := d.resolveFrom(from, path)
	if err != nil || !p.exists || p.entry == nil {
		return 0, false
	}

	entries, ok := astnode.Entries(astnode.Content(p.value))
	if !ok || len(entries) == 0 || isFlow(astnode.Content(p.value)) {
		return 0, false
	}

	keySpan, ok := d.nodeSpan(p.entry.Key)
	if !ok {
		return 0, false
	}

	firstSpan, ok := d.nodeSpan(entries[0].Key)
	if !ok {
		return 0, false
	}

	step := d.column(firstSpan.start) - d.column(keySpan.start)
	if step <= 0 {
		return 0, false
	}

	return step, true
}

// docStep returns the step of the first block mapping in the document that
// is the value of an entry, or two spaces when the document does not have
// one. It leaves out skip and the nodes inside it, the entry or the value
// that an edit takes out of the document, or nil.
func (d *Doc) docStep(skip ast.Node) int {
	step := 0

	d.inspect(func(n ast.Node) bool {
		if step > 0 || skip != nil && n == skip {
			return false
		}

		e, ok := n.(*ast.MappingValueNode)
		if !ok {
			return true
		}

		entries, ok := astnode.Entries(astnode.Content(e.Value))
		if !ok || len(entries) == 0 || isFlow(astnode.Content(e.Value)) {
			return true
		}

		keySpan, ok1 := d.nodeSpan(e.Key)
		firstSpan, ok2 := d.nodeSpan(entries[0].Key)

		if ok1 && ok2 {
			if s := d.column(firstSpan.start) - d.column(keySpan.start); s > 0 {
				step = s
			}
		}

		return true
	})

	if step == 0 {
		return defaultStep
	}

	return step
}

// sequenceIndent reports whether a block sequence that an edit creates
// under a key indents its "-" one step deeper than the key. It follows the
// first block sequence under a key in the user's document, so a removal of
// that sequence does not change the style of the edits after it. When the
// user's document does not have one, it reports true. The Doc keeps the
// answer, which the user's document alone decides.
func (d *Doc) sequenceIndent() bool {
	if d.seqIndent == 0 {
		d.seqIndent = -1
		if d.readSequenceIndent() {
			d.seqIndent = 1
		}
	}

	return d.seqIndent > 0
}

// readSequenceIndent reads what [Doc.sequenceIndent] tells from the user's
// document.
func (d *Doc) readSequenceIndent() bool {
	indent, found := true, false

	astnode.Inspect(d.user.AST(), func(n ast.Node) bool {
		if found {
			return false
		}

		e, ok := n.(*ast.MappingValueNode)
		if !ok || astnode.IsNil(e.Key) {
			return true
		}

		seq, ok := astnode.Content(e.Value).(*ast.SequenceNode)
		if !ok || seq.IsFlowStyle || seq.Start == nil || seq.Start.Position == nil {
			return true
		}

		if key := e.Key.GetToken(); key != nil && key.Position != nil {
			indent, found = seq.Start.Position.Column > key.Position.Column, true
		}

		return true
	})

	return indent
}

// breakBefore returns the line break that ends a line that an edit adds at
// the start of line. It takes the break of the line above, and otherwise
// the break of the first line that has one. A text without a line break
// takes the first line break of the user's document or of its file, so
// a text that an edit shrinks to one line keeps its line breaks. It
// returns "\n" when neither has one.
func (d *Doc) breakBefore(line int) string {
	if line > 0 {
		if b := d.lineBreak(line - 1); b != "" {
			return b
		}
	}

	for l := range d.curLines {
		if b := d.lineBreak(l); b != "" {
			return b
		}
	}

	return d.brk
}

// firstBreak returns the first line break of text, or "" when text does
// not have one.
func firstBreak(text string) string {
	i := strings.IndexAny(text, "\r\n")

	switch {
	case i < 0:
		return ""
	case strings.HasPrefix(text[i:], "\r\n"):
		return "\r\n"
	default:
		return text[i : i+1]
	}
}

// indentLines returns lines, each after col spaces, joined with brk.
func indentLines(lines []string, col int, brk string) string {
	pad := strings.Repeat(" ", col)

	var b strings.Builder

	for i, l := range lines {
		if i > 0 {
			b.WriteString(brk)
		}

		if l != "" {
			b.WriteString(pad)
		}

		b.WriteString(l)
	}

	return b.String()
}
